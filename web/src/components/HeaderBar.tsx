import {
  Cpu,
  FolderPlus,
  Loader2,
  Pause,
  Play,
  RefreshCw,
  Settings,
  Sparkles,
  Square,
  Wifi,
  WifiOff,
  Recycle,
} from 'lucide-react';
import type { BrowserKokoroState } from '../hooks/useBrowserKokoro';
import type { HardwareProfile, RunnerStatus, TTSHealthStatus } from '../types/api';

interface HeaderBarProps {
  runnerStatus: RunnerStatus;
  singleTargetId?: string;
  reuseAudioChunks: boolean;
  ttsStatus: TTSHealthStatus | null;
  browserKokoroState: BrowserKokoroState;
  browserKokoroProgress: number;
  browserKokoroDevice: string;
  browserKokoroError: string;
  hardware: HardwareProfile | null;
  checkingTTS: boolean;
  hasPendingJobs: boolean;
  onToggleReuseChunks: (val: boolean) => void;
  onToggleBrowserKokoro: () => void;
  onStart: () => void;
  onPause: () => void;
  onStop: () => void;
  onOpenBrowser: () => void;
  onOpenSettings: () => void;
  onCheckTTS: () => void;
}

export function HeaderBar({
  runnerStatus,
  singleTargetId,
  reuseAudioChunks,
  ttsStatus,
  browserKokoroState,
  browserKokoroProgress,
  browserKokoroDevice,
  browserKokoroError,
  hardware,
  checkingTTS,
  hasPendingJobs,
  onToggleReuseChunks,
  onToggleBrowserKokoro,
  onStart,
  onPause,
  onStop,
  onOpenBrowser,
  onOpenSettings,
  onCheckTTS,
}: HeaderBarProps) {
  const isRunning = runnerStatus === 'running';
  const isRunningSingle = isRunning && Boolean(singleTargetId);
  const isPaused = runnerStatus === 'paused';
  const isTransitioning = runnerStatus === 'pausing' || runnerStatus === 'stopping';

  return (
    <header className="sticky top-0 z-20 border-b border-slate-800/80 bg-slate-950/90 backdrop-blur-md">
      <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-4 px-4 py-3.5 sm:px-6">
        {/* Brand + Status Pills */}
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex items-center gap-2.5">
            <img
              src="/logo.png"
              alt="Go-Dubber Logo"
              className="h-9 w-9 rounded-xl object-cover shadow-sm ring-1 ring-indigo-500/30"
            />
            <div>
              <h1 className="text-base font-semibold tracking-tight text-white">Go-Dubber</h1>
              <p className="text-xs text-slate-400">AI Media Dubbing Studio</p>
            </div>
          </div>

          <div className="h-6 w-px bg-slate-800 hidden sm:block" />

          {/* TTS Health Pill */}
          <button
            type="button"
            onClick={onCheckTTS}
            title={
              ttsStatus?.error ||
              (ttsStatus?.transport === 'grpc'
                ? `TTS gRPC: ${ttsStatus.grpc_url} (HTTP fallback: ${ttsStatus.url})`
                : `TTS HTTP: ${ttsStatus?.url || 'checking...'}${ttsStatus?.grpc_url ? ` (gRPC ${ttsStatus.grpc_url} unavailable)` : ''}`)
            }
            className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium ring-1 transition ${
              ttsStatus?.available
                ? 'bg-emerald-500/10 text-emerald-300 ring-emerald-500/30 hover:bg-emerald-500/20'
                : 'bg-rose-500/10 text-rose-300 ring-rose-500/30 hover:bg-rose-500/20'
            }`}
          >
            {ttsStatus?.available ? (
              <Wifi className="h-3.5 w-3.5" />
            ) : (
              <WifiOff className="h-3.5 w-3.5" />
            )}
            <span>
              TTS{' '}
              {ttsStatus?.available
                ? `Online · ${ttsStatus.transport === 'grpc' ? 'gRPC' : 'HTTP'} (${ttsStatus.latency_ms}ms)`
                : 'Offline'}
            </span>
            <RefreshCw className={`h-3 w-3 opacity-70 ${checkingTTS ? 'animate-spin' : ''}`} />
          </button>

          {/* In-Browser KokoroJS English Fallback Pill */}
          <button
            type="button"
            onClick={onToggleBrowserKokoro}
            title={
              browserKokoroError ||
              'Run Kokoro-82M (ONNX) directly in this browser tab as an offline English TTS fallback'
            }
            className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium ring-1 transition ${
              browserKokoroState === 'ready'
                ? 'bg-violet-500/15 text-violet-200 ring-violet-500/40 hover:bg-violet-500/25'
                : browserKokoroState === 'loading'
                ? 'bg-amber-500/15 text-amber-200 ring-amber-500/40'
                : browserKokoroState === 'error'
                ? 'bg-rose-500/15 text-rose-300 ring-rose-500/40'
                : 'bg-slate-800/80 text-slate-400 ring-slate-700 hover:bg-slate-800 hover:text-slate-200'
            }`}
          >
            {browserKokoroState === 'loading' ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Sparkles className="h-3.5 w-3.5" />
            )}
            <span>
              {browserKokoroState === 'ready'
                ? `Browser EN (${browserKokoroDevice.toUpperCase()})`
                : browserKokoroState === 'loading'
                ? `Loading EN ${browserKokoroProgress}%`
                : browserKokoroState === 'error'
                ? 'Browser EN Error'
                : 'Browser EN: Off'}
            </span>
          </button>

          {/* Detected Hardware Badge */}
          {hardware && (
            <div
              title={`Detected ${hardware.os}/${hardware.arch} (${hardware.cpu_cores} cores) • Available: ${hardware.available_encoders?.join(', ')}`}
              className="inline-flex items-center gap-1.5 rounded-full bg-slate-800/80 px-3 py-1 text-xs font-medium text-slate-300 ring-1 ring-slate-700"
            >
              <Cpu className="h-3.5 w-3.5 text-sky-400" />
              <span>{hardware.encoder_label}</span>
            </div>
          )}
        </div>

        {/* Controls */}
        <div className="flex flex-wrap items-center gap-2.5">
          {/* Reuse Audio Chunks Toggle */}
          <label
            title="Reuse cached TTS audio chunks from previous or paused runs instead of re-synthesizing"
            className="inline-flex cursor-pointer items-center gap-2 rounded-lg border border-slate-800 bg-slate-900 px-3 py-1.5 text-xs text-slate-300 hover:border-slate-700"
          >
            <Recycle className={`h-3.5 w-3.5 ${reuseAudioChunks ? 'text-emerald-400' : 'text-slate-500'}`} />
            <span className="select-none">Reuse Chunks</span>
            <button
              type="button"
              role="switch"
              aria-checked={reuseAudioChunks}
              onClick={() => onToggleReuseChunks(!reuseAudioChunks)}
              className={`relative inline-flex h-4 w-7 shrink-0 items-center rounded-full transition-colors ${
                reuseAudioChunks ? 'bg-emerald-500' : 'bg-slate-700'
              }`}
            >
              <span
                className={`inline-block h-3 w-3 transform rounded-full bg-white transition-transform ${
                  reuseAudioChunks ? 'translate-x-3.5' : 'translate-x-0.5'
                }`}
              />
            </button>
          </label>

          {/* Add Files / Folders */}
          <button
            type="button"
            onClick={onOpenBrowser}
            className="inline-flex items-center gap-1.5 rounded-lg bg-slate-800 px-3 py-1.5 text-xs font-medium text-slate-100 ring-1 ring-slate-700 transition hover:bg-slate-700"
          >
            <FolderPlus className="h-3.5 w-3.5 text-indigo-400" />
            Add Files / Folder
          </button>

          {/* Start / Resume / Upgrade Single Run to Start All */}
          {(!isRunning || isRunningSingle) && (
            <button
              type="button"
              disabled={isTransitioning || !hasPendingJobs}
              onClick={onStart}
              title={
                isRunningSingle
                  ? 'Continue running all remaining pending tasks after the current item finishes (skips completed tasks)'
                  : 'Start processing all pending tasks in the queue (skips completed tasks)'
              }
              className="inline-flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3.5 py-1.5 text-xs font-semibold text-white shadow-sm transition hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-40"
            >
              <Play className="h-3.5 w-3.5 fill-current" />
              {isRunningSingle ? 'Start All' : isPaused ? 'Resume Queue' : 'Start Queue'}
            </button>
          )}

          {/* Pause */}
          {isRunning && (
            <button
              type="button"
              disabled={isTransitioning}
              onClick={onPause}
              className="inline-flex items-center gap-1.5 rounded-lg bg-amber-500/20 px-3 py-1.5 text-xs font-semibold text-amber-300 ring-1 ring-amber-500/40 transition hover:bg-amber-500/30 disabled:opacity-40"
            >
              <Pause className="h-3.5 w-3.5" />
              Pause
            </button>
          )}

          {/* Stop */}
          {(isRunning || isPaused || isTransitioning) && (
            <button
              type="button"
              disabled={runnerStatus === 'stopping'}
              onClick={onStop}
              className="inline-flex items-center gap-1.5 rounded-lg bg-rose-500/20 px-3 py-1.5 text-xs font-semibold text-rose-300 ring-1 ring-rose-500/40 transition hover:bg-rose-500/30 disabled:opacity-40"
            >
              <Square className="h-3.5 w-3.5 fill-current" />
              Stop
            </button>
          )}

          {/* Global Settings */}
          <button
            type="button"
            onClick={onOpenSettings}
            title="Global Settings & Hardware Profile"
            className="inline-flex items-center justify-center rounded-lg border border-slate-800 bg-slate-900 p-1.5 text-slate-300 transition hover:bg-slate-800 hover:text-white"
          >
            <Settings className="h-4 w-4" />
          </button>
        </div>
      </div>
    </header>
  );
}
