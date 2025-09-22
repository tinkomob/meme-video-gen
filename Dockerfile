# Use an official Python runtime as a parent image
FROM python:3.11-slim

# Set the working directory in the container
WORKDIR /app

# Install system dependencies for moviepy (ffmpeg), Node.js and Git
RUN apt-get update && apt-get install -y \
    ffmpeg \
    git \
    curl \
    wget \
    gnupg \
    fonts-liberation \
    fonts-noto \
    fonts-noto-color-emoji \
    libxkbcommon0 \
    libxshmfence1 \
    libxss1 \
    libu2f-udev \
    libglib2.0-0 \
    libnss3 \
    libx11-6 \
    libx11-xcb1 \
    libxcomposite1 \
    libxcursor1 \
    libxdamage1 \
    libxi6 \
    libxext6 \
    libxfixes3 \
    libxrandr2 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libgbm1 \
    libasound2 \
    libpangocairo-1.0-0 \
    libpango-1.0-0 \
    libcairo2 \
    libgtk-3-0 \
    xdg-utils \
    unzip \
    ca-certificates \
    libgstreamer1.0-0 \
    libgstreamer-plugins-base1.0-0 \
    libgtk-4-1 \
    libgraphene-1.0-0 \
    libicu76 \
    libxslt1.1 \
    libwoff1 \
    libevent-2.1-7 \
    libwebpdemux2 \
    libavif16 \
    libharfbuzz-icu0 \
    libenchant-2-2 \
    libsecret-1-0 \
    libhyphen0 \
    libmanette-0.2-0 \
    gstreamer1.0-plugins-base \
    gstreamer1.0-plugins-good \
    gstreamer1.0-plugins-bad \
    gstreamer1.0-plugins-ugly \
    gstreamer1.0-libav \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js (required for TiktokAutoUploader)
RUN curl -fsSL https://deb.nodesource.com/setup_18.x | bash - && \
    apt-get install -y nodejs && \
    rm -rf /var/lib/apt/lists/*

# Copy the requirements file into the container
COPY requirements.txt .

# Install Python dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Pre-install playwright chromium browser for signature generation
ENV PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS=1
RUN npm i -g playwright-chromium@1.10.0 && \
    npx playwright install chromium

# Copy the rest of the application code
COPY . .

# Copy environment file into image (optional: adjust if building in CI)
# COPY .env .env

# Load environment variables during runtime via python-dotenv or explicit ENV if desired
# Example: uncomment to bake specific variables (avoid for secrets in public builds)
# ENV TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}

# Install Node.js dependencies for TikTok signer at build-time
RUN if [ -d "app/vendor/tiktok_uploader/tiktok-signature" ]; then \
            cd app/vendor/tiktok_uploader/tiktok-signature && \
            npm install --no-audit --no-fund; \
        fi

# Set environment for TiktokAutoUploader (requests-based, no browser needed)
ENV TIKTOK_HEADLESS=true \
    PYTHONUNBUFFERED=1 \
    NODE_ENV=production

# Run the Telegram bot
CMD ["python", "bot.py"]