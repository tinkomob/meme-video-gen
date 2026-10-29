struct Params {
    size_time: vec4<f32>,
    audio: vec4<f32>,
}

@group(0) @binding(0) var<uniform> p: Params;
@group(0) @binding(1) var<storage, read_write> pixels: array<u32>;

@compute @workgroup_size(8, 8)
fn main(@builtin(global_invocation_id) id: vec3<u32>) {
    let w = u32(p.size_time.x);
    let h = u32(p.size_time.y);
    if (id.x >= w || id.y >= h) { return; }
    let uv = vec2<f32>(f32(id.x) / f32(w), f32(id.y) / f32(h));
    let t = p.size_time.z;
    let q = (uv - vec2<f32>(0.5)) * vec2<f32>(1.15, 2.05);
    let beat = clamp(p.size_time.w * 2.5, 0.0, 1.0);

    // Three offset flow fields read as separate liquid sheets at different depths.
    let far = q * 0.72 + vec2<f32>(sin(t * 0.21), cos(t * 0.17)) * 0.13;
    let farFlow = sin(far.x * 7.0 + t * 0.26) * cos(far.y * 4.5 - t * 0.19);
    let farVein = 1.0 - smoothstep(0.015, 0.13, abs(farFlow));

    let mid = q * 1.08 + vec2<f32>(cos(t * 0.31), sin(t * 0.23)) * 0.09;
    let midBend = vec2<f32>(sin(mid.y * 4.0 - t * 0.48), cos(mid.x * 3.5 + t * 0.35));
    let midFlow = sin(mid.x * 8.0 + midBend.x * 2.2 + t * 0.36)
        * cos(mid.y * 5.0 + midBend.y * 1.8 - t * 0.25);
    let midVein = 1.0 - smoothstep(0.015, 0.15, abs(midFlow));

    let near = q * 1.52 + vec2<f32>(sin(t * 0.39), -cos(t * 0.29)) * 0.055;
    let nearBend = vec2<f32>(sin(near.y * 4.5 - t * 0.65), cos(near.x * 3.8 + t * 0.44));
    let nearFlow = sin(near.x * 8.2 + nearBend.x * 2.2 + t * 0.52)
        * cos(near.y * 5.2 + nearBend.y * 1.8 - t * 0.34);
    let ripple = sin(near.x * 13.0 - near.y * 4.0 + t * 0.48 + nearFlow * 2.0);
    let nearVein = 1.0 - smoothstep(0.0, 0.17 + p.audio.x * 0.05, abs(nearFlow + ripple * 0.12));
    let cloud = 1.0 - smoothstep(0.10, 0.95, abs(midFlow));
    let edge = 1.0 - smoothstep(0.015, 0.13 + p.audio.y * 0.08, abs(nearFlow + ripple * 0.12));
    let vignette = 1.0 - smoothstep(0.18, 1.35, length(q * vec2<f32>(0.78, 0.65)));

    let deep = vec3<f32>(0.006, 0.012, 0.055);
    let teal = vec3<f32>(0.015, 0.22, 0.34);
    let blue = vec3<f32>(0.045, 0.09, 0.52);
    let pink = vec3<f32>(0.48, 0.035, 0.34);
    var color = deep + vec3<f32>(0.13, 0.035, 0.31) * farVein * 0.45;
    color += teal * cloud * 0.72 + blue * midVein * 0.48;
    color += vec3<f32>(0.02, 0.27, 0.38) * nearVein * 0.62 + pink * edge * 0.28;
    let glow = pow(nearVein, 3.0) * (0.45 + p.audio.z * 0.5 + beat * 0.25);
    color += vec3<f32>(0.04, 0.62, 1.0) * glow * 1.6;

    // The beat recolors two moving pools, leaving the rest of the flow unchanged.
    let poolA = 1.0 - smoothstep(0.06, 0.34, length(q - vec2<f32>(sin(t * 0.37) * 0.28, cos(t * 0.29) * 0.62)));
    let poolB = 1.0 - smoothstep(0.05, 0.29, length(q - vec2<f32>(cos(t * 0.24 + 1.7) * 0.34, sin(t * 0.32) * 0.72)));
    let beatColor = clamp(beat * max(poolA, poolB) * (0.45 + nearVein * 0.75), 0.0, 1.0);
    color = mix(color, vec3<f32>(1.0, 0.12, 0.48), beatColor * 0.9);
    color *= vignette;
    color = color / (1.0 + color);
    color = pow(color, vec3<f32>(0.72));
    let c = vec3<u32>(round(clamp(color, vec3<f32>(0.0), vec3<f32>(1.0)) * 255.0));
    pixels[id.y * w + id.x] = c.r | (c.g << 8u) | (c.b << 16u) | 0xff000000u;
}
