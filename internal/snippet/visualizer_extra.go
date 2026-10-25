package snippet

import "math"

func (p *snippetPainter) tunnel(t float64, a audioFrame) {
	for ring := 0; ring < 18; ring++ {
		depth := math.Mod(float64(ring)/18+t*.045, 1)
		radius := 25 + depth*560*p.spread
		var lastX, lastY float64
		for step := 0; step <= 240; step++ {
			f := float64(step) / 240
			angle := f*2*math.Pi + p.phase + t*.08
			energy := bandAt(a, f+float64(ring)*.017)
			r := radius * (1 + .07*energy)
			r *= 1 + .035*p.morph*math.Sin(angle*3+depth*5+t*.2)
			if p.variant == 1 {
				r *= math.Pow(math.Pow(math.Abs(math.Cos(angle)), 6)+math.Pow(math.Abs(math.Sin(angle)), 6), -1.0/6)
			}
			if p.variant == 2 {
				r *= 1 + .14*math.Cos(angle*5+t*.3+depth*4)
			}
			x := 270 + r*math.Cos(angle) + 35*depth*math.Sin(t*.2+p.phase)
			y := 450 + r*1.35*math.Sin(angle)
			if step > 0 {
				p.line(lastX, lastY, x, y, mixLight(p.palette[0], p.palette[1], depth), (.12+.5*depth)*(.5+energy))
			}
			lastX, lastY = x, y
		}
	}
}

func (p *snippetPainter) orbit(t float64, a audioFrame) {
	for ring := 0; ring < 11; ring++ {
		tilt := float64(ring)*math.Pi/11 + p.phase + t*.09
		var lastX, lastY float64
		for step := 0; step <= 300; step++ {
			f := float64(step) / 300
			angle := f*2*math.Pi + t*.12
			energy := bandAt(a, f+float64(ring)*.021)
			radius := (220 + 25*energy + 12*a.level) * p.spread
			x, y := radius*math.Cos(angle), radius*(.16+.28*p.morph)*math.Sin(angle)
			if p.variant == 1 {
				x *= .65 + float64(ring)*.065
				y += 40 * math.Sin(angle*3+t*.4)
			}
			if p.variant == 2 {
				y *= 2.2
				x += 30 * math.Sin(angle*5+tilt)
			}
			x, y = 270+x*math.Cos(tilt)-y*math.Sin(tilt), 440+(x*math.Sin(tilt)+y*math.Cos(tilt))*1.35
			if step > 0 {
				p.line(lastX, lastY, x, y, p.palette[ring%3], .22+.4*energy)
			}
			lastX, lastY = x, y
		}
	}
}

func (p *snippetPainter) wave(t float64, a audioFrame) {
	for row := 0; row < 30; row++ {
		depth := float64(row) / 29
		var lastX, lastY float64
		for step := 0; step <= 160; step++ {
			f := float64(step) / 160
			energy := bandAt(a, f+depth*.15)
			x := 270 + (f-.5)*(140+depth*620)*p.spread
			y := 290 + depth*570 + (15+55*depth+65*energy)*(.6+.8*p.morph)*math.Sin(f*9-depth*(3+4*p.morph)+t*.7+p.phase)
			if p.variant == 1 {
				y += (35 + 70*energy) * (.5 + p.morph) * math.Cos(f*15+depth*8-t*.5)
			}
			if p.variant == 2 {
				y = 180 + depth*620 + (30+100*energy)*(.6+.8*p.morph)*math.Sin(f*6+t*.5+p.phase)*math.Cos(depth*(4+5*p.morph)-t*.3)
			}
			color := mixLight(p.palette[0], p.palette[1], depth)
			if step > 0 {
				p.line(lastX, lastY, x, y, color, .15+.35*depth+.25*energy)
			}
			if step%8 == 0 {
				p.point(x, y, p.palette[2], .4+energy)
			}
			lastX, lastY = x, y
		}
	}
}

func (p *snippetPainter) rain(t float64, a audioFrame) {
	for i, s := range p.particles {
		energy := bandAt(a, s.u)
		speed := 25 + s.depth*90
		x := s.u*620 - 40 + 18*math.Sin(t*.3+s.phase)*p.spread
		y := math.Mod(s.v*1100+t*speed, 1100) - 70
		dx, dy := 0.0, (5+18*s.depth+20*energy)*(.6+.8*p.morph)
		if p.variant == 1 {
			x += (y-480)*.32 + 15*math.Sin(p.phase)
			dx = dy * .32
		}
		if p.variant == 2 {
			y = 960 - y
			x += 30 * math.Sin(y*.014+t*.4+p.phase)
			dx, dy = -dy*.25*math.Cos(y*.014+t*.4+p.phase), -dy
		}
		color := mixLight(p.palette[0], p.palette[1], s.depth)
		brightness := (.1 + s.depth*.6) * (.35 + .65*energy + .3*a.level)
		p.line(x, y, x-dx, y-dy, color, brightness*.45)
		p.point(x, y, p.palette[2], brightness*1.5)
		if i%9 == 0 {
			p.line(x-2, y, x+2, y, color, brightness)
		}
	}
}
