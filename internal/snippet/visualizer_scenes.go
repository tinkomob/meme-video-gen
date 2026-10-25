package snippet

import "math"

func (p *snippetPainter) aurora(t float64, a audioFrame) {
	for curtain := 0; curtain < 2; curtain++ {
		phase := p.phase + float64(curtain)*1.7
		for strand := 0; strand < 170; strand++ {
			x := float64(strand)*3.5 - 25
			energy := bandAt(a, float64(strand)/170)
			base := 190 + float64(curtain)*550 + 65*math.Sin(x*.009+t*.25+phase)
			if p.variant == 1 {
				base += 45 * math.Sin(x*.021-t*.3+phase)
			}
			if p.variant == 2 {
				base += x*.22 - 70
			}
			height := (40 + 100*energy + 25*a.level) * p.spread * (.7 + .6*p.morph)
			color := mixLight(p.palette[0], p.palette[1], .5+.5*math.Sin(x*.007+phase))
			lastX, lastY := x, base
			for step := 1; step <= 16; step++ {
				f := float64(step) / 16
				xx := x + 14*math.Sin(f*3+t*.25+phase)*f
				yy := base - height*f
				p.line(lastX, lastY, xx, yy, mixLight(color, p.palette[2], f*.6), (.12+.2*energy)*(1-f*.65))
				lastX, lastY = xx, yy
			}
			p.point(x, base, color, .7+energy)
		}
		for layer := 0; layer < 5; layer++ {
			lastX, lastY := 0.0, 0.0
			for step := 0; step <= 360; step++ {
				x := float64(step) * 540 / 360
				y := 190 + float64(curtain)*550 + 65*math.Sin(x*.009+t*.25+phase) + float64(layer)*4
				if p.variant == 1 {
					y += 45 * math.Sin(x*.021-t*.3+phase)
				}
				if p.variant == 2 {
					y += x*.22 - 70
				}
				y += 12 * bandAt(a, x/540) * math.Sin(x*.025-t*.4+float64(layer))
				if step > 0 {
					p.line(lastX, lastY, x, y, p.palette[layer%3], .2)
				}
				lastX, lastY = x, y
			}
		}
	}
	for _, s := range p.particles {
		x, y := s.u*540, math.Mod(s.v*960-t*(1+3*s.depth)+960, 960)
		p.point(x, y, mixLight(p.palette[1], p.palette[2], s.depth), (.05+.4*s.depth)*(.4+.6*a.level))
	}
}

func (p *snippetPainter) prism(t float64, a audioFrame) {
	sides := []int{6, 3, 4}[p.variant]
	for layer := 0; layer < 9; layer++ {
		rotation := p.phase + t*.07 + float64(layer)*(.025+.065*p.morph)
		if p.variant == 1 {
			rotation += float64(layer/3) * math.Pi / 3
		}
		radius := (265 + float64(layer)*7) * p.spread
		var firstX, firstY, lastX, lastY float64
		for vertex := 0; vertex <= sides; vertex++ {
			angle := rotation + float64(vertex%sides)*2*math.Pi/float64(sides)
			energy := bandAt(a, float64(vertex%sides)/float64(sides)+float64(layer)*.013)
			r := radius * (1 + .1*energy)
			x := 270 + r*math.Cos(angle)
			y := 470 + r*1.22*math.Sin(angle)
			color := mixLight(p.palette[0], p.palette[1], float64(layer)/9)
			if vertex == 0 {
				firstX, firstY = x, y
			} else {
				p.line(lastX, lastY, x, y, color, .5+.35*energy)
			}
			p.point(x, y, p.palette[2], 1.2+energy)
			lastX, lastY = x, y
		}
		if layer%3 == 0 {
			angle := rotation + 2*math.Pi/float64(sides) + .045
			x := 270 + (radius+20)*math.Cos(angle)
			y := 470 + (radius+20)*1.22*math.Sin(angle)
			p.line(firstX, firstY, x, y, p.palette[2], .3+.2*a.level)
		}
	}
	for i, s := range p.particles {
		if i < 22 {
			angle := s.u*2*math.Pi + t*.035
			radius := 290 + s.v*100 + 25*bandAt(a, s.u)
			x, y := 270+radius*math.Cos(angle), 470+radius*1.2*math.Sin(angle)
			size := 5 + 12*s.depth + 8*a.level
			for edge := 0; edge < 3; edge++ {
				a0, a1 := s.phase+t*.1+float64(edge)*2*math.Pi/3, s.phase+t*.1+float64(edge+1)*2*math.Pi/3
				p.line(x+size*math.Cos(a0), y+size*math.Sin(a0), x+size*math.Cos(a1), y+size*math.Sin(a1), p.palette[i%3], .65)
			}
		} else {
			p.point(s.u*540, math.Mod(s.v*960+t*(s.depth-0.5)*3+960, 960), p.palette[i%3], (.06+.35*s.depth)*(.4+.6*a.level))
		}
	}
}

func (p *snippetPainter) galaxy(t float64, a audioFrame) {
	arms := 2 + p.variant
	for arm := 0; arm < arms; arm++ {
		for filament := 0; filament < 9; filament++ {
			lastX, lastY := 0.0, 0.0
			for step := 0; step <= 420; step++ {
				f := float64(step) / 420
				energy := bandAt(a, f)
				r := 75 + f*430 + energy*12
				angle := p.phase + float64(arm)*2*math.Pi/float64(arms) + f*(3.5+2.2*p.morph) + t*.035 + float64(filament)*.025 + energy*.015
				x, y := 270+r*math.Cos(angle)*p.spread, 470+r*1.25*math.Sin(angle)
				color := mixLight(p.palette[0], p.palette[1], f)
				if step > 0 {
					p.line(lastX, lastY, x, y, color, (.2+.3*energy)*(1-f*.5))
				}
				lastX, lastY = x, y
			}
		}
	}
	for i, s := range p.particles {
		energy := bandAt(a, s.u)
		if i%3 == 0 {
			p.point(s.u*540, math.Mod(s.v*960+t*(s.depth-.5)*2+960, 960), p.palette[2], (.1+s.depth*.6)*(.4+.6*a.level))
			continue
		}
		f := s.u
		r := 75 + f*430 + (s.v-.5)*65 + energy*12
		angle := p.phase + float64(i%arms)*2*math.Pi/float64(arms) + f*(3.5+2.2*p.morph) + t*(.025+s.depth*.02) + (s.v-.5)*.22 + energy*.015
		x, y := 270+r*math.Cos(angle)*p.spread, 470+r*1.25*math.Sin(angle)
		color := mixLight(p.palette[0], p.palette[1], f)
		brightness := (.15 + s.depth*1.5) * (.4 + .6*energy)
		p.point(x, y, color, brightness)
		if s.depth > .97 {
			p.line(x-1.5, y, x+1.5, y, p.palette[2], brightness)
			p.line(x, y-1.5, x, y+1.5, p.palette[2], brightness)
		}
		if p.variant == 2 && s.depth > .92 {
			p.line(x, y, x+5*math.Sin(angle), y-6*math.Cos(angle), color, brightness*.25)
		}
	}
}
