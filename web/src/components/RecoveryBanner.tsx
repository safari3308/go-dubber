import { AlertTriangle, HardDrive, RotateCcw, Trash2 } from 'lucide-react';
import { formatBytes } from '../api/client';
import type { RecoverableJob } from '../types/api';

interface RecoveryBannerProps {
  jobs: RecoverableJob[];
  onResume: (jobId: string) => void;
  onDiscard: (jobId: string) => void;
}

export function RecoveryBanner({ jobs, onResume, onDiscard }: RecoveryBannerProps) {
  if (jobs.length === 0) return null;

  return (
    <div className="rounded-xl border border-amber-500/30 bg-amber-500/10 p-4">
      <div className="flex items-start gap-3">
        <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-amber-400" />
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-semibold text-amber-200">
            Unfinished Work Detected ({jobs.length} {jobs.length === 1 ? 'job' : 'jobs'})
          </h2>
          <p className="mt-0.5 text-xs text-amber-200/80">
            Found interrupted dubbing sessions in your local working directory. Resume them to skip
            re-copying large files from NAS and reuse rendered TTS audio chunks.
          </p>

          <div className="mt-3 space-y-2">
            {jobs.map((rj) => {
              const fileName = rj.state.source_video_path.split(/[/\\]/).pop() || rj.state.job_id;
              return (
                <div
                  key={rj.state.job_id}
                  className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/20 bg-slate-950/60 px-3 py-2.5"
                >
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-medium text-slate-100">{fileName}</div>
                    <div className="mt-1 flex flex-wrap items-center gap-3 text-xs text-slate-400">
                      <span className="inline-flex items-center gap-1">
                        <HardDrive className="h-3 w-3 text-emerald-400" />
                        {rj.local_video_valid
                          ? `Verified local copy (${formatBytes(rj.local_video_size)})`
                          : rj.local_video_exists
                          ? `Partial local copy (${formatBytes(rj.local_video_size)})`
                          : 'No local copy'}
                      </span>
                      <span>•</span>
                      <span>Cached TTS chunks: {rj.total_chunks}</span>
                      <span>•</span>
                      <span className="capitalize">Last stage: {rj.state.stage}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={() => onResume(rj.state.job_id)}
                      className="inline-flex items-center gap-1.5 rounded-lg bg-amber-500 px-3 py-1.5 text-xs font-semibold text-slate-950 transition hover:bg-amber-400"
                    >
                      <RotateCcw className="h-3.5 w-3.5" />
                      Resume
                    </button>
                    <button
                      type="button"
                      onClick={() => onDiscard(rj.state.job_id)}
                      className="inline-flex items-center gap-1.5 rounded-lg bg-slate-800 px-2.5 py-1.5 text-xs font-medium text-rose-300 ring-1 ring-slate-700 transition hover:bg-rose-500/20"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      Discard
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
