# Go-Dubber

An automated NAS media dubbing pipeline that scans video files, extracts subtitles, synthesizes AI voiceovers using [Kokoro TTS](https://github.com/iamdinhthuan/Kokoro-Vietnamese), and remuxes the final video with dubbed audio tracks — all fully unattended.

## Features

- 🎬 **Automated NAS Scanning**: Recursively walks a NAS directory for `.mp4`, `.mkv`, `.avi` video files.
- 💬 **Smart Subtitle Discovery**: Finds external subtitle files (`.srt`, `.ass`, `.vtt`) in Vietnamese or English, with fallback to embedded subtitle extraction.
- 🔄 **Subtitle Timeline Sync**: Synchronizes subtitles against reference audio via the [tts-service](https://github.com/safari3308/tts-service) subsync endpoint.
- 🎙️ **Parallel AI Voice Rendering**: Sends each dialogue line to the Kokoro TTS server for concurrent voice synthesis with configurable worker count.
- ⚡ **GPU-Accelerated Encoding**: 2-pass FFmpeg architecture — CPU audio pre-mixing followed by GPU video encoding (NVENC) or ultra-fast stream copy for already-optimized files.
- 🛡️ **Safe NAS Replacement**: Atomic file swap with data integrity checks ensures no data loss during network transfers.
- 📊 **Execution Metrics**: Reports AI dubbing time, remux/transcode time, total duration, and file size savings.

## Architecture

```
NAS Video File
    │
    ├── 1. Fast metadata probe (ffprobe)
    ├── 2. Subtitle discovery (external → embedded)
    ├── 3. Download video to local SSD
    ├── 4. Extract anchor audio track
    ├── 5. Sync subtitle timeline (via tts-service API)
    ├── 6. Parallel AI voice rendering (via tts-service API)
    ├── 7. 2-pass FFmpeg remux (audio mix → video encode)
    └── 8. Safe NAS replacement with integrity verification
```

## Prerequisites

- [Go](https://go.dev/) 1.20+
- [FFmpeg](https://ffmpeg.org/) with `ffprobe` (for NVENC GPU encoding, an NVIDIA GPU with appropriate drivers is required)
- A running instance of [tts-service](https://github.com/safari3308/tts-service) for TTS and subtitle sync

## Configuration

Copy and customize the example config:

```bash
cp config.example.json config.json
```

| Field              | Description                                      |
|--------------------|--------------------------------------------------|
| `nas_dir`          | Path to the NAS directory containing video files |
| `api_url`          | TTS service API base URL                         |
| `api_token`        | Bearer token for API authentication              |
| `use_gpu`          | Enable NVIDIA NVENC GPU encoding                 |
| `workers`          | Number of concurrent TTS rendering workers       |
| `force_reprocess`  | Re-process videos that already have Kokoro tracks|
| `ffmpeg`           | FFmpeg encoding parameters (preset, CRF/CQ, etc)|

## Build & Run

```bash
go build -o nas_dubber.exe
./nas_dubber.exe
```

## License

[MIT](LICENSE)