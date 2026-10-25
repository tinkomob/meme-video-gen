package snippet

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"strconv"
)

const (
	visualWidth  = 540
	visualHeight = 960
	visualFPS    = 30
	visualBands  = 64
)

type audioFrame struct {
	bands [visualBands]float64
	level float64
}

// Analyze the same excerpt that is encoded, including its fades.
func snippetAudio(ctx context.Context, path string, start float64) ([]audioFrame, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-ss", fmt.Sprintf("%.6f", start), "-i", path,
		"-t", strconv.Itoa(DurationSeconds), "-af", snippetAudioFade(), "-ac", "1", "-ar", "16000", "-f", "s16le", "pipe:1")
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("decode visualizer audio: %w", err)
	}
	if len(data) < 16000*2*DurationSeconds-3200 {
		return nil, fmt.Errorf("decoded audio is shorter than the snippet")
	}
	samples := make([]float64, len(data)/2)
	for i := range samples {
		samples[i] = float64(int16(binary.LittleEndian.Uint16(data[i*2:]))) / 32768
	}
	return analyzeSnippet(samples), nil
}

// Goertzel measures logarithmic frequency bands without another dependency.
func analyzeSnippet(samples []float64) []audioFrame {
	const window = 1024
	var coefficients [visualBands]float64
	var hann [window]float64
	for b := range coefficients {
		frequency := 40 * math.Pow(7000.0/40, float64(b)/(visualBands-1))
		coefficients[b] = 2 * math.Cos(2*math.Pi*frequency/16000)
	}
	for i := range hann {
		hann[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/(window-1))
	}
	frames := make([]audioFrame, DurationSeconds*visualFPS)
	peak, peakLevel := 1e-9, 1e-9
	for f := range frames {
		var block [window]float64
		for i := range block {
			index := f*16000/visualFPS + i - window/2
			if index >= 0 && index < len(samples) {
				block[i] = samples[index] * hann[i]
				frames[f].level += block[i] * block[i]
			}
		}
		frames[f].level = math.Sqrt(frames[f].level / window)
		peakLevel = math.Max(peakLevel, frames[f].level)
		for b, coefficient := range coefficients {
			var q1, q2 float64
			for _, sample := range block {
				q0 := sample + coefficient*q1 - q2
				q2, q1 = q1, q0
			}
			magnitude := math.Sqrt(math.Max(0, q1*q1+q2*q2-coefficient*q1*q2)) / window
			frames[f].bands[b] = magnitude
			peak = math.Max(peak, magnitude)
		}
	}
	var previous audioFrame
	for f := range frames {
		frames[f].level = frames[f].level / peakLevel
		for b, value := range frames[f].bands {
			value = math.Log1p(60*value/peak) / math.Log(61)
			weight := 0.65
			if value < previous.bands[b] {
				weight = 0.2
			}
			frames[f].bands[b] = previous.bands[b] + weight*(value-previous.bands[b])
		}
		previous = frames[f]
	}
	return frames
}

type lightColor struct{ r, g, b float32 }
type visualParticle struct{ u, v, depth, phase float64 }

type snippetPainter struct {
	style              string
	variant            int
	palette            [3]lightColor
	speed, spread      float64
	phase              float64
	base, pixels, glow []float32
	shade              []float32
	rgb                []byte
	particles          []visualParticle
	timeline           [5]visualKeyframe
	view               visualView
	morph              float64
}

func newSnippetPainter(style string, seed int64, variant int) *snippetPainter {
	r := rand.New(rand.NewSource(seed))
	if variant < 0 {
		variant = r.Intn(3)
	}
	p := &snippetPainter{style: style, variant: variant, palette: snippetPalettes[style][variant],
		speed: .75 + r.Float64()*.5, spread: .88 + r.Float64()*.24, phase: r.Float64() * 2 * math.Pi,
		base: make([]float32, visualWidth*visualHeight*3), pixels: make([]float32, visualWidth*visualHeight*3),
		shade: make([]float32, visualWidth*visualHeight),
		glow:  make([]float32, (visualWidth/4)*(visualHeight/4)*3), rgb: make([]byte, visualWidth*visualHeight*3),
		particles: make([]visualParticle, 1800)}
	for i := range p.particles {
		p.particles[i] = visualParticle{r.Float64(), r.Float64(), r.Float64(), r.Float64() * 2 * math.Pi}
	}
	p.initEvolution(r)
	p.view = visualView{xx: 1, yy: 1, light: 1, focus: 1}
	for y := 0; y < visualHeight; y++ {
		for x := 0; x < visualWidth; x++ {
			d := math.Hypot((float64(x)-270)/430, (float64(y)-480)/700)
			v := float32(math.Max(0, 1-d))
			c := mixLight(lightColor{2, 3, 5}, p.palette[0], float64(.012+.025*v))
			i := (y*visualWidth + x) * 3
			dx, dy := (float64(x)-270)/205, (float64(y)-620)/95
			p.shade[i/3] = 1 - .87*float32(math.Exp(-dx*dx*dx*dx-dy*dy*dy*dy))
			p.base[i], p.base[i+1], p.base[i+2] = c.r, c.g, c.b
		}
	}
	return p
}

func mixLight(a, b lightColor, t float64) lightColor {
	f := float32(t)
	return lightColor{a.r + (b.r-a.r)*f, a.g + (b.g-a.g)*f, a.b + (b.b-a.b)*f}
}

// Bilinear light points keep subpixel motion smooth; a separate small light
// buffer gives particles and filaments a broad bloom without blurring their cores.
func (p *snippetPainter) point(x, y float64, c lightColor, intensity float64) {
	x, y = p.view.project(x, y)
	p.lightPoint(x, y, c, intensity*p.view.light)
}

func (p *snippetPainter) lightPoint(x, y float64, c lightColor, intensity float64) {
	if x < -16 || y < -16 || x >= visualWidth+16 || y >= visualHeight+16 {
		return
	}
	ix, iy := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := float32(x-float64(ix)), float32(y-float64(iy))
	for dy := 0; dy <= 1; dy++ {
		for dx := 0; dx <= 1; dx++ {
			xx, yy := ix+dx, iy+dy
			if xx < 0 || xx >= visualWidth || yy < 0 || yy >= visualHeight {
				continue
			}
			wx, wy := 1-fx, 1-fy
			if dx == 1 {
				wx = fx
			}
			if dy == 1 {
				wy = fy
			}
			a := wx * wy * float32(intensity*p.view.focus)
			i := (yy*visualWidth + xx) * 3
			p.pixels[i] += c.r * a
			p.pixels[i+1] += c.g * a
			p.pixels[i+2] += c.b * a
		}
	}
	gx, gy := int(x/4), int(y/4)
	for dy := -3; dy <= 3; dy++ {
		for dx := -3; dx <= 3; dx++ {
			xx, yy := gx+dx, gy+dy
			if xx < 0 || xx >= visualWidth/4 || yy < 0 || yy >= visualHeight/4 {
				continue
			}
			a := float32(intensity*0.025) / float32(1+dx*dx+dy*dy)
			i := (yy*(visualWidth/4) + xx) * 3
			p.glow[i] += c.r * a
			p.glow[i+1] += c.g * a
			p.glow[i+2] += c.b * a
		}
	}
}

func (p *snippetPainter) line(x0, y0, x1, y1 float64, c lightColor, intensity float64) {
	x0, y0 = p.view.project(x0, y0)
	x1, y1 = p.view.project(x1, y1)
	if math.Max(x0, x1) < -16 || math.Min(x0, x1) >= visualWidth+16 || math.Max(y0, y1) < -16 || math.Min(y0, y1) >= visualHeight+16 {
		return
	}
	intensity *= p.view.light
	steps := max(1, int(math.Ceil(math.Hypot(x1-x0, y1-y0))))
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		p.lightPoint(x0+(x1-x0)*t, y0+(y1-y0)*t, c, intensity)
	}
}

func bandAt(a audioFrame, position float64) float64 {
	x := math.Mod(math.Abs(position), 1) * (visualBands - 1)
	i := int(x)
	f := x - float64(i)
	f = f * f * (3 - 2*f)
	return a.bands[i] + (a.bands[min(i+1, visualBands-1)]-a.bands[i])*f
}

func (p *snippetPainter) frame(frame int, audio audioFrame) []byte {
	copy(p.pixels, p.base)
	clear(p.glow)
	seconds := float64(frame) / visualFPS
	pose := p.evolution(float64(frame) / (DurationSeconds*visualFPS - 1))
	p.atmosphere(seconds, pose, audio)
	p.depthLayers(seconds, pose, audio)
	p.depthParticles(seconds, pose, audio)
	for y := 0; y < visualHeight; y++ {
		for x := 0; x < visualWidth; x++ {
			i, gi := (y*visualWidth+x)*3, ((y/4)*(visualWidth/4)+x/4)*3
			for c := 0; c < 3; c++ {
				p.rgb[i+c] = byte(math.Min(255, float64((p.pixels[i+c]+p.glow[gi+c])*p.shade[i/3])))
			}
		}
	}
	return p.rgb
}

func (p *snippetPainter) scene(t float64, audio audioFrame) {
	switch p.style {
	case "silver":
		p.silver(t, audio)
	case "violet":
		p.violet(t, audio)
	case "amber":
		p.amber(t, audio)
	case "aurora":
		p.aurora(t, audio)
	case "prism":
		p.prism(t, audio)
	case "galaxy":
		p.galaxy(t, audio)
	case "tunnel":
		p.tunnel(t, audio)
	case "orbit":
		p.orbit(t, audio)
	case "wave":
		p.wave(t, audio)
	case "rain":
		p.rain(t, audio)
	}
}

func (p *snippetPainter) silver(t float64, a audioFrame) {
	white := p.palette[0]
	for i := 0; i < 110; i++ {
		x := float64(i)*5 - 4
		wave := 660 + 55*math.Sin(x*.012+t*.35+p.phase)
		height := 35 + 210*bandAt(a, float64(i)/110) + 60*a.level + 140*math.Pow(math.Abs(x-270)/270, 1.4)
		height *= .75 + .45*p.morph
		if p.variant == 1 {
			wave = 720 + 35*math.Cos(x*.018-t*.3+p.phase)
			height *= p.spread * (1 + .22*math.Sin(x*.023+t*.4))
		}
		if p.variant == 2 {
			wave = 610 + 70*math.Sin(x*.009+t*.25+p.phase)
			height *= .6 + .35*math.Pow(math.Sin(x*.015+p.phase), 2)
		}
		p.line(x, wave-height, x, wave, white, .65)
		p.line(x, wave+8, x, wave+height*.3+8, white, .08)
		p.point(x, wave-height, white, 1.7)
		if p.variant == 1 {
			p.line(x, wave+10, x, wave+height*.55+10, p.palette[1], .2)
		}
		if p.variant == 2 {
			for j := 0; j < 5; j++ {
				y := math.Mod(float64(i*19+j*97)+t*(20+50*a.level), 340)
				p.line(x, y+180, x, y+184+10*bandAt(a, float64(i)/110), p.palette[1], .35)
			}
		}
	}
	for layer := 0; layer < 7; layer++ {
		lastX, lastY := 0.0, 0.0
		for i := 0; i <= 400; i++ {
			x := float64(i) * visualWidth / 400
			y := 720 + float64(layer)*10*p.spread + 38*math.Sin(x*.014+t*.35+p.phase) + 20*math.Sin(x*.028-t*.5) + 26*bandAt(a, x/540)
			if i > 0 {
				p.line(lastX, lastY, x, y, mixLight(white, p.palette[1], float64(layer)/7), .17)
			}
			lastX, lastY = x, y
		}
	}
	for _, s := range p.particles {
		x := s.u * 540
		base := 700 + 45*math.Sin(x*.014+t*.35+p.phase)
		y := base + (s.v-.5)*280 + 8*math.Sin(t*(.2+s.depth)+s.phase)
		brightness := (.15 + s.depth*.8) * (.35 + a.level*.65)
		p.point(x, y, white, brightness)
		if s.depth > .97 {
			p.line(x-1.5, y, x+1.5, y, white, brightness)
			p.line(x, y-2, x, y+2, white, brightness)
		}
		p.point(x, 830+(830-y)*.35, white, brightness*.16)
	}
}

func (p *snippetPainter) violet(t float64, a audioFrame) {
	violet, blue := p.palette[0], p.palette[1]
	for layer := 0; layer < 12; layer++ {
		lastX, lastY := 0.0, 0.0
		for i := 0; i <= 720; i++ {
			angle := float64(i) / 720 * 2 * math.Pi
			energy := bandAt(a, float64(i)/720)
			r := 1 + .035*math.Sin(angle*7+t*.8+p.phase+float64(layer)*.4) + .02*math.Sin(angle*17-t) + .012*math.Sin(angle*37+t*1.1+float64(layer)*.85) + .006*math.Cos(angle*61-t*.9+float64(layer)) + energy*.09
			r += .055 * p.morph * math.Sin(angle*4+p.phase+float64(layer)*.15)
			if p.variant == 1 {
				r += .09 * math.Sin(angle*3+t*.3+p.phase+float64(layer/6)*math.Pi)
			}
			if p.variant == 2 {
				r += (.065 + .035*a.level) * math.Sin(angle*6-t*.35+p.phase)
			}
			x := 270 + (250+float64(layer)*1.5)*r*math.Cos(angle)*p.spread
			y := 470 + (340+float64(layer)*1.5)*r*math.Sin(angle)
			color := mixLight(violet, blue, .5+.5*math.Sin(angle*2+t*.3))
			if i > 0 {
				p.line(lastX, lastY, x, y, color, .13+.19*energy)
			}
			lastX, lastY = x, y
		}
	}
	for _, s := range p.particles {
		angle := s.u*2*math.Pi + t*(.01+s.depth*.025)
		energy := bandAt(a, s.u)
		r := 1 + (s.v-.5)*.28 + energy*.07 + .025*math.Sin(angle*7+t+p.phase)
		if p.variant == 1 {
			r += .09 * math.Sin(angle*3+t*.3+p.phase)
		}
		if p.variant == 2 {
			r += (.065 + .035*a.level) * math.Sin(angle*6-t*.35+p.phase)
		}
		x, y := 270+267*r*math.Cos(angle)*p.spread, 470+360*r*math.Sin(angle)
		c := mixLight(violet, blue, .5+.5*math.Sin(angle*2+t*.3))
		p.point(x, y, c, (.15+s.depth*.8)*(.4+.6*a.level))
		if s.depth > .98 {
			p.line(x-1, y, x+1, y, c, 1)
			p.line(x, y-1, x, y+1, c, 1)
		}
	}
}

func (p *snippetPainter) amber(t float64, a audioFrame) {
	gold, coral, teal := p.palette[0], p.palette[1], p.palette[2]
	for stream := 0; stream < 2; stream++ {
		phase := p.phase + float64(stream)*1.8
		for strand := 0; strand < 15; strand++ {
			lastX, lastY := 0.0, 0.0
			for i := 0; i <= 450; i++ {
				x := -40 + float64(i)*620/450
				energy := bandAt(a, float64(i)/450)
				spread := float64(strand-7) * (2 + 3*math.Sin(x*.008+t*.3+phase)) * (.65 + .7*p.morph)
				y := 100 + float64(stream)*600 + x*.38 + 45*math.Sin(x*.011+t*.4+phase) + spread + (14+energy*18)*math.Sin(x*.018-t*.5+float64(strand)*.27) + 8*math.Sin(x*.033+t*.3+float64(strand)*.45)
				if p.variant == 1 {
					y = 190 + float64(stream)*560 + 95*math.Sin(x*.01+t*.4+phase) + spread*1.7 + (10+25*energy)*math.Sin(x*.018-t*.4+float64(strand)*.32)
				}
				if p.variant == 2 {
					y = 150 + float64(stream)*570 - x*.13 + 70*math.Sin(x*.007+t*.25+phase) + spread*2.1 + 20*energy*math.Sin(x*.02+float64(strand)*.3)
				}
				c := mixLight(gold, coral, .35+.3*math.Sin(x*.006+float64(stream)))
				if strand < 2 {
					c = teal
					y += 80
				}
				if i > 0 {
					p.line(lastX, lastY, x, y, c, .22+.2*energy)
				}
				lastX, lastY = x, y
			}
		}
	}
	for _, s := range p.particles {
		x := math.Mod(s.u*620+t*(2+s.depth*6), 620) - 40
		stream := math.Floor(s.v * 2)
		y := 100 + stream*600 + x*.38 + 45*math.Sin(x*.011+t*.4+p.phase+stream*1.8) + (math.Mod(s.v*2, 1)-.5)*170
		if p.variant == 1 {
			y = 190 + stream*560 + 95*math.Sin(x*.01+t*.4+p.phase+stream*1.8) + (math.Mod(s.v*2, 1)-.5)*210
		}
		if p.variant == 2 {
			y = 150 + stream*570 - x*.13 + 70*math.Sin(x*.007+t*.25+p.phase+stream*1.8) + (math.Mod(s.v*2, 1)-.5)*210
		}
		p.point(x, y, mixLight(gold, coral, s.depth), (.1+.8*s.depth)*(.4+.6*a.level))
	}
}
