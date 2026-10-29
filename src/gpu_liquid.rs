use anyhow::{Context, Result, ensure};
use bytemuck::{Pod, Zeroable};
use std::sync::mpsc;

const WIDTH: u32 = 540;
const HEIGHT: u32 = 960;

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct Params {
    size_time: [f32; 4],
    audio: [f32; 4],
}

pub struct Liquid {
    device: wgpu::Device,
    queue: wgpu::Queue,
    pipeline: wgpu::ComputePipeline,
    bind_group: wgpu::BindGroup,
    params: wgpu::Buffer,
    output: wgpu::Buffer,
    readback: wgpu::Buffer,
}

impl Liquid {
    pub fn new() -> Result<Self> {
        let instance = wgpu::Instance::default();
        let adapter = pollster::block_on(instance.request_adapter(&wgpu::RequestAdapterOptions {
            power_preference: wgpu::PowerPreference::HighPerformance,
            force_fallback_adapter: false,
            compatible_surface: None,
            ..Default::default()
        }))
        .context("no hardware GPU adapter available for liquid shader")?;
        let info = adapter.get_info();
        ensure!(
            info.device_type != wgpu::DeviceType::Cpu,
            "selected adapter is not a GPU"
        );
        tracing::info!(gpu = %info.name, backend = ?info.backend, "GPU liquid renderer initialized");
        let (device, queue) =
            pollster::block_on(adapter.request_device(&wgpu::DeviceDescriptor {
                label: Some("liquid shader device"),
                ..Default::default()
            }))?;
        let params = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("liquid parameters"),
            size: std::mem::size_of::<Params>() as u64,
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        let bytes = (WIDTH * HEIGHT * 4) as u64;
        let output = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("liquid pixels"),
            size: bytes,
            usage: wgpu::BufferUsages::STORAGE | wgpu::BufferUsages::COPY_SRC,
            mapped_at_creation: false,
        });
        let readback = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("liquid readback"),
            size: bytes,
            usage: wgpu::BufferUsages::COPY_DST | wgpu::BufferUsages::MAP_READ,
            mapped_at_creation: false,
        });
        let shader = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("glowing liquid WGSL"),
            source: wgpu::ShaderSource::Wgsl(include_str!("gpu_liquid.wgsl").into()),
        });
        let pipeline = device.create_compute_pipeline(&wgpu::ComputePipelineDescriptor {
            label: Some("liquid compute pipeline"),
            layout: None,
            module: &shader,
            entry_point: Some("main"),
            compilation_options: Default::default(),
            cache: None,
        });
        let bind_group = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("liquid bindings"),
            layout: &pipeline.get_bind_group_layout(0),
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: params.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: output.as_entire_binding(),
                },
            ],
        });
        Ok(Self {
            device,
            queue,
            pipeline,
            bind_group,
            params,
            output,
            readback,
        })
    }

    pub fn frame(&self, index: usize, audio: crate::visualizer::AudioFrame) -> Result<Vec<u8>> {
        self.queue.write_buffer(
            &self.params,
            0,
            bytemuck::bytes_of(&Params {
                size_time: [
                    WIDTH as f32,
                    HEIGHT as f32,
                    index as f32 / 30.0,
                    audio.beat as f32,
                ],
                audio: [
                    audio.bass as f32,
                    audio.treble as f32,
                    audio.level as f32,
                    audio.attacks[0] as f32,
                ],
            }),
        );
        let mut encoder = self
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("liquid frame"),
            });
        {
            let mut pass = encoder.begin_compute_pass(&wgpu::ComputePassDescriptor {
                label: Some("liquid pixels"),
                timestamp_writes: None,
            });
            pass.set_pipeline(&self.pipeline);
            pass.set_bind_group(0, &self.bind_group, &[]);
            pass.dispatch_workgroups(WIDTH.div_ceil(8), HEIGHT.div_ceil(8), 1);
        }
        encoder.copy_buffer_to_buffer(
            &self.output,
            0,
            &self.readback,
            0,
            (WIDTH * HEIGHT * 4) as u64,
        );
        self.queue.submit([encoder.finish()]);
        let slice = self.readback.slice(..);
        let (tx, rx) = mpsc::channel();
        slice.map_async(wgpu::MapMode::Read, move |result| {
            let _ = tx.send(result);
        });
        self.device
            .poll(wgpu::PollType::Wait {
                submission_index: None,
                timeout: None,
            })
            .context("wait for liquid shader")?;
        rx.recv()
            .context("GPU readback callback")?
            .context("map GPU frame")?;
        let mapped = slice
            .get_mapped_range()
            .context("access mapped GPU frame")?;
        let mut rgb = Vec::with_capacity((WIDTH * HEIGHT * 3) as usize);
        for pixel in mapped.chunks_exact(4) {
            rgb.extend_from_slice(&pixel[..3]);
        }
        drop(mapped);
        self.readback.unmap();
        Ok(rgb)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn gpu_frame_has_expected_rgb_shape_and_visible_color() {
        let gpu = Liquid::new().expect("test requires a hardware GPU");
        let calm = crate::visualizer::AudioFrame::default();
        let frame = gpu.frame(30, calm).unwrap();
        assert_eq!(frame.len(), (WIDTH * HEIGHT * 3) as usize);
        assert!(frame.chunks_exact(3).any(|p| p[1] > p[0] && p[2] > p[0]));
        let beat = crate::visualizer::AudioFrame { beat: 1.0, ..calm };
        assert_ne!(frame, gpu.frame(30, beat).unwrap());
    }
}
