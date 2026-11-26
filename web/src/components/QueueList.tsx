import { useRef, useState } from 'react';
import {
  ArrowDown,
  ArrowUp,
  CheckCircle2,
  Clock,
  FileVideo,
  Loader2,
  PauseCircle,
  Play,
  Sliders,
  Trash2,
  Upload,
  X,
  XCircle,
} from 'lucide-react';
import { formatBytes, formatDuration } from '../api/client';
import type { AvailableLanguageInfo, JobItem, JobStatus } from '../types/api';

interface QueueListProps {
  items: JobItem[];
  activeJobId?: string;
  runnerRunning: boolean;
  onStartSingle: (id: string) => void;
  onEdit: (job: JobItem) => void;
  onRemove: (id: string) => void;
  onMove: (index: number, dir: -1 | 1) => void;
  onToggleLanguage: (job: JobItem, lang: 'en' | 'vi', checked: boolean) => Promise<void>;
  onUploadSubtitle: (
    jobId: string,
    file: File,
    lang: 'en' | 'vi',
    role: 'both' | 'tts' | 'embed'
  ) => Promise<void>;
}

const SUPPORTED_LANGS: { code: 'en' | 'vi'; label: string }[] = [
  { code: 'en', label: 'English' },
  { code: 'vi', label: 'Vietnamese' },
];

const statusStyles: Record<JobStatus, { label: string; cls: string }> = {
  pending: { label: 'Queued', cls: 'bg-slate-800 text-slate-300 ring-slate-700' },
  downloading: { label: 'Downloading', cls: 'bg-sky-500/15 text-sky-300 ring-sky-500/30' },
  syncing: { label: 'Syncing Sub', cls: 'bg-violet-500/15 text-violet-300 ring-violet-500/30' },
  synthesizing: { label: 'TTS Voice', cls: 'bg-indigo-500/15 text-indigo-300 ring-indigo-500/30' },
  remuxing: { label: 'Remuxing', cls: 'bg-fuchsia-500/15 text-fuchsia-300 ring-fuchsia-500/30' },
  uploading: { label: 'Uploading NAS', cls: 'bg-cyan-500/15 text-cyan-300 ring-cyan-500/30' },
  completed: { label: 'Completed', cls: 'bg-emerald-500/15 text-emerald-300 ring-emerald-500/30' },
  paused: { label: 'Paused', cls: 'bg-amber-500/15 text-amber-300 ring-amber-500/30' },
  failed: { label: 'Failed', cls: 'bg-rose-500/15 text-rose-300 ring-rose-500/30' },
  cancelled: { label: 'Cancelled', cls: 'bg-slate-800 text-slate-400 ring-slate-700' },
};

function guessLangFromFilename(name: string): 'en' | 'vi' {
  const lower = name.toLowerCase();
  if (
    lower.includes('.en.') ||
    lower.includes('_en') ||
    lower.includes('-en') ||
    lower.includes('eng') ||
    lower.includes('english')
  ) {
    return 'en';
  }
  return 'vi';
}

interface PendingUpload {
  jobId: string;
  file: File | null;
  lang: 'en' | 'vi';
  role: 'both' | 'tts' | 'embed';
}

export function QueueList({
  items,
  activeJobId,
  runnerRunning,
  onStartSingle,
  onEdit,
  onRemove,
  onMove,
  onToggleLanguage,
  onUploadSubtitle,
}: QueueListProps) {
  const [dragOverJobId, setDragOverJobId] = useState<string | null>(null);
  const [pendingUpload, setPendingUpload] = useState<PendingUpload | null>(null);
  const [uploading, setUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  if (items.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-slate-800 bg-slate-900/40 px-6 py-16 text-center">
        <FileVideo className="h-10 w-10 text-slate-600" />
        <h3 className="mt-3 text-sm font-semibold text-slate-200">Queue is empty</h3>
        <p className="mt-1 max-w-sm text-xs text-slate-400">
          Click <span className="font-medium text-indigo-400">Add Files / Folder</span> or paste a
          macOS pathname above. You can tick EN/VI languages and drag-and-drop external subtitles
          directly onto any item.
        </p>
      </div>
    );
  }

  const openUploadTray = (job: JobItem, droppedFile?: File) => {
    const guessedLang = droppedFile ? guessLangFromFilename(droppedFile.name) : 'vi';
    const isAss = droppedFile ? /\.(ass|ssa)$/i.test(droppedFile.name) : false;
    setPendingUpload({
      jobId: job.id,
      file: droppedFile || null,
      lang: guessedLang,
      role: isAss ? 'both' : 'both',
    });
  };

  const handleConfirmUpload = async () => {
    if (!pendingUpload || !pendingUpload.file) return;
    setUploading(true);
    try {
      await onUploadSubtitle(
        pendingUpload.jobId,
        pendingUpload.file,
        pendingUpload.lang,
        pendingUpload.role
      );
      setPendingUpload(null);
    } finally {
      setUploading(false);
    }
  };

  return (
    <div className="space-y-3">
      {items.map((job, idx) => {
        const isActive = job.id === activeJobId;
        const style = statusStyles[job.status] || statusStyles.pending;
        const isRunningStage =
          job.status === 'downloading' ||
          job.status === 'syncing' ||
          job.status === 'synthesizing' ||
          job.status === 'remuxing' ||
          job.status === 'uploading';
        const isDragTarget = dragOverJobId === job.id;
        const showUploadTray = pendingUpload?.jobId === job.id;

        const availMap = new Map<string, AvailableLanguageInfo>();
        for (const al of job.available_languages || []) {
          availMap.set(al.language, al);
        }

        return (
          <div
            key={job.id}
            onDragOver={(e) => {
              if (isActive && isRunningStage) return;
              e.preventDefault();
              e.dataTransfer.dropEffect = 'copy';
              if (dragOverJobId !== job.id) setDragOverJobId(job.id);
            }}
            onDragLeave={(e) => {
              if (!e.currentTarget.contains(e.relatedTarget as Node)) {
                setDragOverJobId((curr) => (curr === job.id ? null : curr));
              }
            }}
            onDrop={(e) => {
              e.preventDefault();
              setDragOverJobId(null);
              if (isActive && isRunningStage) return;
              const dropped = e.dataTransfer.files?.[0];
              if (dropped) {
                openUploadTray(job, dropped);
              }
            }}
            className={`rounded-xl border p-4 transition ${
              isDragTarget
                ? 'border-indigo-400 bg-indigo-500/10 ring-2 ring-indigo-500/40'
                : isActive
                ? 'border-indigo-500/50 bg-slate-900 shadow-lg shadow-indigo-500/5'
                : 'border-slate-800/90 bg-slate-900/60 hover:border-slate-700'
            }`}
          >
            <div className="flex flex-wrap items-start justify-between gap-3">
              {/* Left info */}
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span
                    className={`inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-[11px] font-medium ring-1 ${style.cls}`}
                  >
                    {isRunningStage && <Loader2 className="h-3 w-3 animate-spin" />}
                    {job.status === 'completed' && <CheckCircle2 className="h-3 w-3" />}
                    {job.status === 'failed' && <XCircle className="h-3 w-3" />}
                    {job.status === 'paused' && <PauseCircle className="h-3 w-3" />}
                    {job.status === 'pending' && <Clock className="h-3 w-3" />}
                    {style.label}
                  </span>
                  <h4 className="truncate text-sm font-semibold text-white">{job.file_name}</h4>
                </div>

                <p className="mt-1 truncate text-xs text-slate-500">{job.source_path}</p>

                {/* TTS Language Checkboxes (EN / VI) + Metadata */}
                <div className="mt-2.5 flex flex-wrap items-center gap-2 text-xs">
                  {SUPPORTED_LANGS.map(({ code, label }) => {
                    const info = availMap.get(code);
                    const checked = (job.config.dub_languages || []).includes(code);
                    const available = info ? info.available : true;
                    const spec = job.config.language_specs?.find((s) => s.language === code);
                    const hasASS =
                      spec?.embed_sub?.type === 'external' &&
                      (spec.embed_sub.format === 'ass' || spec.embed_sub.format === 'ssa');

                    const sourceTags: string[] = [];
                    if (info?.has_embedded) sourceTags.push('Emb');
                    if (info?.has_external) sourceTags.push('Ext');
                    if (info?.has_uploaded) sourceTags.push('Up');

                    const hasExistingAudio = Boolean(info?.has_existing_audio);
                    const replaceIdx =
                      spec?.replace_audio_index !== undefined &&
                      spec?.replace_audio_index !== null &&
                      spec.replace_audio_index >= 0
                        ? spec.replace_audio_index
                        : null;

                    return (
                      <label
                        key={code}
                        title={
                          hasExistingAudio
                            ? `${label} audio already exists — unchecked by default. Tick to add a new track or replace an existing track in Config.`
                            : available
                            ? `${label} subtitles found (${sourceTags.join(', ') || 'configured'})`
                            : `No ${label} subtitle found yet — upload or drop one to enable`
                        }
                        className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-[11px] transition ${
                          checked
                            ? 'border-indigo-500/50 bg-indigo-500/15 text-indigo-200'
                            : available
                            ? 'border-slate-700 bg-slate-800/80 text-slate-300 hover:border-slate-600'
                            : 'border-slate-800 bg-slate-950/50 text-slate-500'
                        } ${isActive && isRunningStage ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'}`}
                      >
                        <input
                          type="checkbox"
                          disabled={isActive && isRunningStage}
                          checked={checked}
                          onChange={(e) => void onToggleLanguage(job, code, e.target.checked)}
                          className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-indigo-500 focus:ring-indigo-500"
                        />
                        <span className="font-semibold uppercase">{code}</span>
                        {sourceTags.length > 0 ? (
                          <span className="text-[10px] text-slate-400">
                            ({sourceTags.join('+')})
                          </span>
                        ) : (
                          <span className="text-[10px] text-slate-600">(no sub)</span>
                        )}
                        {hasExistingAudio && !checked && (
                          <span className="rounded bg-amber-500/15 px-1 text-[10px] text-amber-300">
                            has audio
                          </span>
                        )}
                        {checked && replaceIdx !== null && (
                          <span className="rounded bg-fuchsia-500/20 px-1 text-[10px] text-fuchsia-300">
                            replace #{replaceIdx}
                          </span>
                        )}
                        {hasASS && (
                          <span className="rounded bg-sky-500/20 px-1 text-[10px] text-sky-300">
                            +ASS
                          </span>
                        )}
                      </label>
                    );
                  })}

                  {job.video_info && job.video_info.audio_track_count === 0 && (
                    <span className="rounded-md bg-emerald-500/15 px-2 py-1 text-[11px] text-emerald-300 ring-1 ring-emerald-500/30">
                      No audio (TTS-only recording)
                    </span>
                  )}
                  {job.video_info && job.video_info.audio_track_count > 0 && (
                    <span
                      title={(job.video_info.audio_streams || [])
                        .map(
                          (a) =>
                            `#${a.audio_index} ${a.language} (${a.codec_name}${a.channels ? `, ${a.channels}ch` : ''})${a.title ? ` — ${a.title}` : ''}`
                        )
                        .join('\n')}
                      className="rounded-md bg-slate-800/70 px-2 py-1 text-[11px] text-slate-400"
                    >
                      {job.video_info.audio_track_count} audio{' '}
                      {job.video_info.audio_track_count === 1 ? 'track' : 'tracks'}
                    </span>
                  )}
                  <span className="rounded-md bg-slate-800/70 px-2 py-1 text-[11px] text-slate-400">
                    {job.config.tts_speed.toFixed(2)}x
                  </span>
                  {job.video_info && (
                    <span className="rounded-md bg-slate-800/70 px-2 py-1 text-[11px] text-slate-400">
                      {job.video_info.video_codec.toUpperCase()} • {job.video_info.width}w •{' '}
                      {formatDuration(job.video_info.duration)}
                    </span>
                  )}
                </div>
              </div>

              {/* Actions */}
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  disabled={runnerRunning}
                  onClick={() => onStartSingle(job.id)}
                  title={
                    runnerRunning
                      ? 'Another task is currently running'
                      : job.status === 'completed'
                      ? 'Re-run only this item'
                      : 'Start only this item'
                  }
                  className="inline-flex items-center gap-1 rounded-lg bg-indigo-600/20 px-2.5 py-1.5 text-xs font-medium text-indigo-300 ring-1 ring-indigo-500/40 hover:bg-indigo-600/35 disabled:cursor-not-allowed disabled:opacity-35"
                >
                  <Play className="h-3 w-3 fill-current" />
                  Run
                </button>
                <button
                  type="button"
                  disabled={idx === 0}
                  onClick={() => onMove(idx, -1)}
                  title="Move up"
                  className="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-white disabled:opacity-30"
                >
                  <ArrowUp className="h-3.5 w-3.5" />
                </button>
                <button
                  type="button"
                  disabled={idx === items.length - 1}
                  onClick={() => onMove(idx, 1)}
                  title="Move down"
                  className="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-white disabled:opacity-30"
                >
                  <ArrowDown className="h-3.5 w-3.5" />
                </button>
                <button
                  type="button"
                  disabled={isActive && isRunningStage}
                  onClick={() => openUploadTray(job)}
                  title="Upload or drop external subtitle (.srt / .ass)"
                  className="inline-flex items-center gap-1 rounded-lg bg-slate-800 px-2.5 py-1.5 text-xs font-medium text-slate-200 ring-1 ring-slate-700 hover:bg-slate-700 disabled:opacity-40"
                >
                  <Upload className="h-3.5 w-3.5 text-emerald-400" />
                  Sub
                </button>
                <button
                  type="button"
                  disabled={isActive && isRunningStage}
                  onClick={() => onEdit(job)}
                  title="Configure dubbing, dual subtitles & extract embedded subs"
                  className="inline-flex items-center gap-1 rounded-lg bg-slate-800 px-2.5 py-1.5 text-xs font-medium text-slate-200 ring-1 ring-slate-700 hover:bg-slate-700 disabled:opacity-40"
                >
                  <Sliders className="h-3.5 w-3.5 text-indigo-400" />
                  Config
                </button>
                <button
                  type="button"
                  onClick={() => onRemove(job.id)}
                  title={isActive ? 'Cancel & remove active job' : 'Remove from queue'}
                  className="rounded-lg p-1.5 text-slate-400 hover:bg-rose-500/20 hover:text-rose-300"
                >
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>
            </div>

            {/* Drag hint banner */}
            {isDragTarget && (
              <div className="mt-3 rounded-lg border border-dashed border-indigo-400/70 bg-indigo-500/15 py-2 text-center text-xs font-medium text-indigo-200">
                Drop subtitle file (.srt, .ass, .ssa, .vtt) here to attach to {job.file_name}
              </div>
            )}

            {/* Inline Upload Subtitle Tray */}
            {showUploadTray && pendingUpload && (
              <div className="mt-3 rounded-xl border border-emerald-500/30 bg-slate-950/80 p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="text-xs font-semibold text-emerald-300">
                    Attach External Subtitle
                  </span>
                  <button
                    type="button"
                    onClick={() => setPendingUpload(null)}
                    className="rounded p-0.5 text-slate-400 hover:text-white"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                </div>

                <div className="mt-2.5 flex flex-wrap items-center gap-2">
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept=".srt,.ass,.ssa,.vtt,.sub,.sbv,.smi,.sami,.ttml,.dfxp,.lrc"
                    onChange={(e) => {
                      const f = e.target.files?.[0] || null;
                      if (f) {
                        setPendingUpload((prev) =>
                          prev ? { ...prev, file: f, lang: guessLangFromFilename(f.name) } : prev
                        );
                      }
                    }}
                    className="hidden"
                  />
                  <button
                    type="button"
                    onClick={() => fileInputRef.current?.click()}
                    className="inline-flex max-w-xs items-center gap-1.5 truncate rounded-lg border border-slate-700 bg-slate-900 px-3 py-1.5 text-xs text-slate-200 hover:border-slate-500"
                  >
                    <Upload className="h-3.5 w-3.5 shrink-0 text-emerald-400" />
                    <span className="truncate">
                      {pendingUpload.file ? pendingUpload.file.name : 'Choose subtitle file...'}
                    </span>
                  </button>

                  <select
                    value={pendingUpload.lang}
                    onChange={(e) =>
                      setPendingUpload((prev) =>
                        prev ? { ...prev, lang: e.target.value as 'en' | 'vi' } : prev
                      )
                    }
                    className="rounded-lg border border-slate-700 bg-slate-900 px-2.5 py-1.5 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
                  >
                    <option value="vi">Vietnamese (VI)</option>
                    <option value="en">English (EN)</option>
                  </select>

                  <select
                    value={pendingUpload.role}
                    onChange={(e) =>
                      setPendingUpload((prev) =>
                        prev
                          ? { ...prev, role: e.target.value as 'both' | 'tts' | 'embed' }
                          : prev
                      )
                    }
                    className="rounded-lg border border-slate-700 bg-slate-900 px-2.5 py-1.5 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
                  >
                    <option value="both">Use for TTS + Embed</option>
                    <option value="tts">Use for TTS Only</option>
                    <option value="embed">Embed in Video Only (ASS/SRT)</option>
                  </select>

                  <button
                    type="button"
                    disabled={!pendingUpload.file || uploading}
                    onClick={() => void handleConfirmUpload()}
                    className="inline-flex items-center gap-1 rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-500 disabled:opacity-40"
                  >
                    {uploading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                    Attach
                  </button>
                </div>
              </div>
            )}

            {/* Progress Bar & Detail */}
            {(isRunningStage || job.progress > 0 || job.detail) && (
              <div className="mt-3">
                <div className="flex items-center justify-between text-xs">
                  <span className="truncate text-slate-300">{job.detail || job.stage}</span>
                  <span className="ml-2 shrink-0 font-mono text-slate-400">
                    {Math.round(job.progress)}%
                  </span>
                </div>
                <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
                  <div
                    className={`h-full transition-all duration-300 ${
                      job.status === 'completed'
                        ? 'bg-emerald-500'
                        : job.status === 'failed'
                        ? 'bg-rose-500'
                        : job.status === 'paused'
                        ? 'bg-amber-500'
                        : 'bg-indigo-500'
                    }`}
                    style={{ width: `${Math.min(100, Math.max(0, job.progress))}%` }}
                  />
                </div>
              </div>
            )}

            {/* Error */}
            {job.error && (
              <div className="mt-2.5 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-1.5 text-xs text-rose-300">
                {job.error}
              </div>
            )}

            {/* Metrics */}
            {job.metrics && (
              <div className="mt-2.5 flex flex-wrap items-center gap-4 text-xs text-emerald-300/90">
                <span>Done in {formatDuration(job.metrics.duration_sec)}</span>
                <span>
                  {formatBytes(job.metrics.orig_size_bytes)} →{' '}
                  {formatBytes(job.metrics.new_size_bytes)}
                </span>
                <span>{job.metrics.completed_dubs} dub track(s)</span>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
