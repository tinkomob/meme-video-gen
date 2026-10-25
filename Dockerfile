FROM rust:1.99-bookworm AS builder
WORKDIR /build
COPY Cargo.toml Cargo.lock ./
COPY src ./src
COPY assets ./assets
RUN cargo build --release --locked --bins

FROM python:3.14-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg fonts-dejavu-core ca-certificates tini \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 memebot \
    && useradd --uid 1000 --gid memebot --create-home memebot
RUN pip install --no-cache-dir "yt-dlp[default]"
COPY --from=denoland/deno:bin-2.9.7 /deno /usr/local/bin/deno
WORKDIR /app
COPY --from=builder /build/target/release/meme-bot /build/target/release/song-snippet /build/target/release/generate-token ./
RUN chown -R memebot:memebot /app
USER memebot
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["./meme-bot"]
