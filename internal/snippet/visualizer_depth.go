package snippet

import (
	"math"
	"math/rand"
)

type visualKeyframe struct {
	at, zoom, turn, x, y, unfold, color, density float64
}

type visualView struct {
	xx, xy, yx, yy, x, y, light, focus float64
}

func (v visualView) project(x, y float64) (float64, float64) {
	x, y = x-270, y-470
	return 270 + v.xx*x + v.xy*y + v.x, 470 + v.yx*x + v.yy*y + v.y
}

func (p *snippetPainter) initEvolution(r *rand.Rand) {
	for i, opening := range [...]float64{.2, .65, .9, .4, .75} {
		at := float64(i) / 4
		if i > 0 && i < 4 {
			at += (r.Float64() - .5) * .1
		}
		p.timeline[i] = visualKeyframe{
			at: at, zoom: .86 + r.Float64()*.28, turn: (r.Float64() - .5) * .28,
			x: (r.Float64() - .5) * 65, y: (r.Float64() - .5) * 80,
			unfold: opening + (r.Float64()-.5)*.18,
			color:  r.Float64() * .6, density: .3 + opening*.55 + r.Float64()*.15,
		}
	}
}

// Absolute snippet progress keeps evolution reproducible, even when frames are rendered out of order.
func (p *snippetPainter) evolution(progress float64) visualKeyframe {
	progress = math.Max(0, math.Min(1, progress))
	if progress == 0 {
		return p.timeline[0]
	}
	if progress == 1 {
		return p.timeline[len(p.timeline)-1]
	}
	for i := 1; i < len(p.timeline); i++ {
		b := p.timeline[i]
		if progress > b.at {
			continue
		}
		a := p.timeline[i-1]
		f := (progress - a.at) / (b.at - a.at)
		f = f * f * (3 - 2*f)
		return visualKeyframe{
			at: progress, zoom: a.zoom + (b.zoom-a.zoom)*f, turn: a.turn + (b.turn-a.turn)*f,
			x: a.x + (b.x-a.x)*f, y: a.y + (b.y-a.y)*f,
			unfold: a.unfold + (b.unfold-a.unfold)*f,
			color:  a.color + (b.color-a.color)*f, density: a.density + (b.density-a.density)*f,
		}
	}
	return p.timeline[len(p.timeline)-1]
}

func (p *snippetPainter) depthLayers(seconds float64, pose visualKeyframe, a audioFrame) {
	palette, phase, spread := p.palette, p.phase, p.spread
	for layer := 0; layer < 3; layer++ {
		depth := .35 + float64(layer)*.325
		angle := pose.turn*depth + (1-depth)*.2*math.Sin(phase+seconds*.12)
		scale := (.68 + float64(layer)*.2) * pose.zoom * (1 + a.level*.015*depth)
		sin, cos := math.Sincos(angle)
		p.view = visualView{
			xx: scale * cos, xy: -scale * sin, yx: scale * sin, yy: scale * cos * (.9 + .1*depth),
			x:     -pose.x*depth + (1-depth)*65*math.Sin(phase+seconds*.17),
			y:     -pose.y*depth + (1-depth)*(55*math.Cos(phase+seconds*.13)-160),
			light: [...]float64{.5, .4, .82}[layer] * (.8 + .3*pose.density),
			focus: [...]float64{.4, .7, 1}[layer],
		}
		for i := range p.palette {
			p.palette[i] = mixLight(palette[i], palette[(i+1)%3], pose.color*.65+(1-depth)*.2)
		}
		p.phase = phase + (1-depth)*1.4 + pose.unfold*.4
		p.spread = spread * (.85 + .3*pose.unfold)
		p.morph = pose.unfold
		p.scene(seconds*p.speed*(.65+.35*depth)+(1-depth)*3, a)
	}
	p.palette, p.phase, p.spread = palette, phase, spread
	p.view = visualView{xx: 1, yy: 1, light: 1, focus: 1}
}

func (p *snippetPainter) atmosphere(seconds float64, pose visualKeyframe, a audioFrame) {
	const width, height = visualWidth / 4, visualHeight / 4
	for cloud := 0; cloud < 3; cloud++ {
		phase := p.phase + float64(cloud)*2.1
		cx := 67 + 48*math.Sin(phase+seconds*.08) - pose.x*.12
		cy := 120 + 95*math.Cos(phase+seconds*.06) - pose.y*.12
		sx, sy := 32+15*pose.unfold, 55+25*math.Sin(phase)*math.Sin(phase)
		c := mixLight(p.palette[cloud], p.palette[(cloud+1)%3], pose.color)
		strength := float32((.04 + .04*pose.density) * (.65 + .35*bandAt(a, float64(cloud)/3)))
		var horizontal [width]float32
		for x := range horizontal {
			dx := (float64(x) - cx) / sx
			horizontal[x] = float32(math.Exp(-dx * dx))
		}
		for y := 0; y < height; y++ {
			dy := (float64(y) - cy) / sy
			vertical := strength * float32(math.Exp(-dy*dy))
			for x := 0; x < width; x++ {
				light := vertical * horizontal[x]
				i := (y*width + x) * 3
				p.glow[i] += c.r * light
				p.glow[i+1] += c.g * light
				p.glow[i+2] += c.b * light
			}
		}
	}
}

func (p *snippetPainter) depthParticles(seconds float64, pose visualKeyframe, a audioFrame) {
	for i, s := range p.particles[:360] {
		z := math.Mod(s.depth+seconds*(.015+.01*s.u), 1)
		perspective := .35 + z*z*1.9
		angle := p.phase + s.phase + seconds*.035
		x := 270 + (s.u-.5)*650*perspective - pose.x*perspective + 12*math.Sin(angle)
		y := 470 + (s.v-.5)*1050*perspective - pose.y*perspective + 20*math.Cos(angle)
		energy := bandAt(a, s.u)
		visibility := math.Sin(z*math.Pi) * math.Max(0, math.Min(1, (pose.density-s.v)*3+.5))
		brightness := visibility * (.15 + .65*energy) * (.25 + z*.6)
		c := mixLight(p.palette[i%3], p.palette[(i+1)%3], pose.color)
		p.view.focus = 1 - .8*z*z
		p.point(x, y, c, brightness)
		if z > .65 {
			trail := math.Min(1, (z-.65)/.15)
			dx, dy := (x-270)*.012*perspective, (y-470)*.012*perspective
			p.line(x-dx, y-dy, x, y, c, brightness*.35*trail)
			p.line(x-2*z, y, x+2*z, y, c, brightness*.35*trail)
			if i%4 == 0 {
				p.softParticle(x, y, 2+5*z*z, c, brightness*.2*trail)
			}
		}
	}
	p.view.focus = 1
}

func (p *snippetPainter) softParticle(x, y, radius float64, c lightColor, intensity float64) {
	const width, height = visualWidth / 4, visualHeight / 4
	x, y = x/4, y/4
	for yy := max(0, int(y-radius)); yy <= min(height-1, int(y+radius)); yy++ {
		for xx := max(0, int(x-radius)); xx <= min(width-1, int(x+radius)); xx++ {
			dx, dy := (float64(xx)-x)/radius, (float64(yy)-y)/radius
			light := float32(intensity * math.Pow(math.Max(0, 1-dx*dx-dy*dy), 2))
			i := (yy*width + xx) * 3
			p.glow[i] += c.r * light
			p.glow[i+1] += c.g * light
			p.glow[i+2] += c.b * light
		}
	}
}
