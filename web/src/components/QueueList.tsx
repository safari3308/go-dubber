import {
  ArrowDown,
  ArrowUp,
  CheckCircle2,
  Clock,
  FileVideo,
  Loader2,
  PauseCircle,
  Sliders,
  Subtitles,
  Trash2,
  XCircle,
} from 'lucide-react';
import { formatBytes, formatDuration } from '../api/client';
import type { JobItem, JobStatus } from '../types/api';

interface QueueListProps {
  items: JobItem[];
  activeJobId?: string;
  onEdit: (job: JobItem) => void;
  onRemove: (id: string) => void;
  onMove: (index: number, dir: -1 | 1) => void;
}

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

export function QueueList({ items, activeJobId, onEdit, onRemove, onMove }: QueueListProps) {
  if (items.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-slate-800 bg-slate-900/40 px-6 py-16 text-center">
        <FileVideo className="h-10 w-10 text-slate-600" />
        <h3 className="mt-3 text-sm font-semibold text-slate-200">Queue is empty</h3>
        <p className="mt-1 max-w-sm text-xs text-slate-400">
          Click <span className="font-medium text-indigo-400">Add Files / Folder</span> to enqueue
          videos. You can configure dual subtitles (TTS + ASS) per video and add or remove items
          even while processing.
        </p>
      </div>
    );
  }

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

        return (
          <div
            key={job.id}
            className={`rounded-xl border p-4 transition ${
              isActive
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

                {/* Config & Dual-Sub Pills */}
                <div className="mt-2.5 flex flex-wrap items-center gap-1.5 text-[11px]">
                  {job.config.dub_languages.map((lang) => {
                    const spec = job.config.language_specs?.find((s) => s.language === lang);
                    const hasSeparateASS =
                      spec?.embed_sub &&
                      spec.embed_sub.type === 'external' &&
                      (spec.embed_sub.format === 'ass' || spec.embed_sub.format === 'ssa');
                    return (
                      <span
                        key={lang}
                        className="inline-flex items-center gap-1 rounded-md bg-slate-800 px-2 py-0.5 text-slate-300 ring-1 ring-slate-700"
                      >
                        <Subtitles className="h-3 w-3 text-indigo-400" />
                        <span className="font-semibold uppercase">{lang}</span>
                        {hasSeparateASS && (
                          <span className="rounded bg-sky-500/20 px-1 text-[10px] text-sky-300">
                            +ASS
                          </span>
                        )}
                      </span>
                    );
                  })}
                  <span className="rounded-md bg-slate-800/70 px-2 py-0.5 text-slate-400">
                    {job.config.tts_speed.toFixed(2)}x
                  </span>
                  {job.config.skip_encode && (
                    <span className="rounded-md bg-slate-800/70 px-2 py-0.5 text-slate-400">
                      Copy Stream
                    </span>
                  )}
                  {job.video_info && (
                    <span className="rounded-md bg-slate-800/70 px-2 py-0.5 text-slate-400">
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
                  onClick={() => onEdit(job)}
                  title="Configure dubbing & dual subtitles"
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
