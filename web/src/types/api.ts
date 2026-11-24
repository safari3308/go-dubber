export interface FFmpegConfig {
  gpu_codec: string;
  gpu_preset: string;
  gpu_cq: number;
  gpu_pix_fmt: string;
  gpu_extra_args?: string[];
  cpu_preset: string;
  cpu_crf: number;
  audio_bitrate: string;
  audio_sample_rate: string;
  audio_channels: string;
  volume_boost: string;
}

export interface GlobalConfig {
  nas_dir: string;
  work_dir?: string;
  server_port?: number;
  api_url: string;
  api_token: string;
  force_fallback_sub: boolean;
  interactive_mode: boolean;
  default_sub_index: number;
  dub_languages: string[];
  sub_language: string;
  original_language: string;
  original_audio_index: number;
  use_gpu: boolean;
  auto_detect_hardware?: boolean;
  workers: number;
  force_reprocess: boolean;
  skip_encode: boolean;
  only_check_kokoro: boolean;
  tts_speed: number;
  ffmpeg: FFmpegConfig;
  skip_sub_sync: boolean;
  ai_track_as_first_track: boolean;
  drop_song_subtitles: boolean;
  reuse_audio_chunks?: boolean;
}

export interface HardwareProfile {
  os: string;
  arch: string;
  cpu_cores: number;
  ffmpeg_available: boolean;
  ffprobe_available: boolean;
  ffmpeg_version: string;
  available_encoders: string[];
  selected_encoder: string;
  encoder_label: string;
  has_gpu: boolean;
  recommended_workers: number;
  recommended_ffmpeg: FFmpegConfig;
}

export interface TTSHealthStatus {
  available: boolean;
  url: string;
  latency_ms: number;
  last_checked: string;
  error?: string;
}

export interface EmbeddedSubInfo {
  sub_index: number;
  stream_idx: number;
  language: string;
  title: string;
  codec_name: string;
  is_bitmap: boolean;
}

export interface AudioStreamDetails {
  audio_index: number;
  is_kokoro: boolean;
  tags: Record<string, string>;
}

export interface VideoInfo {
  video_codec: string;
  is_hevc: boolean;
  is_av1: boolean;
  width: number;
  bitrate: number;
  duration: number;
  is_well_compressed: boolean;
  has_kokoro_track: boolean;
  audio_track_count: number;
  audio_streams: AudioStreamDetails[];
  embedded_sub_streams: EmbeddedSubInfo[];
}

export interface SubtitleSource {
  type: 'external' | 'embedded' | 'none';
  path?: string;
  embedded_index: number;
  format: string;
  description: string;
}

export interface LanguageDubSpec {
  language: string;
  tts_sub: SubtitleSource;
  embed_sub?: SubtitleSource | null;
  sync_tts_sub: boolean;
  sync_embed_sub: boolean;
}

export interface CandidateSubtitle {
  path: string;
  file_name: string;
  format: string;
  language: string;
  score: number;
  is_ass: boolean;
}

export interface ItemConfig {
  dub_languages: string[];
  original_language: string;
  original_audio_index: number;
  tts_speed: number;
  workers: number;
  use_gpu: boolean;
  skip_encode: boolean;
  skip_sub_sync: boolean;
  force_reprocess: boolean;
  ai_track_as_first_track: boolean;
  drop_song_subtitles: boolean;
  volume_boost: string;
  language_specs?: LanguageDubSpec[];
}

export type JobStatus =
  | 'pending'
  | 'downloading'
  | 'syncing'
  | 'synthesizing'
  | 'remuxing'
  | 'uploading'
  | 'completed'
  | 'paused'
  | 'failed'
  | 'cancelled';

export type RunnerStatus = 'idle' | 'running' | 'pausing' | 'paused' | 'stopping';

export interface JobMetrics {
  orig_size_bytes: number;
  new_size_bytes: number;
  duration_sec: number;
  completed_dubs: number;
}

export interface JobItem {
  id: string;
  source_path: string;
  file_name: string;
  status: JobStatus;
  stage: string;
  progress: number;
  detail: string;
  error?: string;
  config: ItemConfig;
  video_info?: VideoInfo;
  metrics?: JobMetrics;
  created_at: string;
  updated_at: string;
}

export interface QueueSnapshot {
  status: RunnerStatus;
  active_job_id?: string;
  reuse_audio_chunks: boolean;
  items: JobItem[];
}

export interface RecoverableJob {
  state: {
    job_id: string;
    source_video_path: string;
    source_size: number;
    stage: string;
    completed_langs: string[];
    updated_at: string;
  };
  job_dir: string;
  local_video_exists: boolean;
  local_video_valid: boolean;
  local_video_size: number;
  chunk_counts: Record<string, number>;
  total_chunks: number;
}

export interface FSEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  is_video: boolean;
  is_subtitle: boolean;
}

export interface FSBrowseResponse {
  path: string;
  parent: string;
  entries: FSEntry[];
}

export interface FSInspectResponse {
  video_info: VideoInfo;
  external_subtitles: Record<string, CandidateSubtitle[]>;
  resolved_specs: LanguageDubSpec[];
}

export interface LogEntry {
  level: string;
  job_id: string;
  message: string;
  timestamp: string;
}

export interface ToastNotification {
  id: string;
  level: 'info' | 'success' | 'warn' | 'error';
  title: string;
  message: string;
}
