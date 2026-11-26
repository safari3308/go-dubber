import { useCallback, useEffect, useState } from 'react';
import { CheckCheck, ClipboardPaste, Plus, Terminal, X } from 'lucide-react';
import { api } from './api/client';
import { FileBrowserModal } from './components/FileBrowserModal';
import { HeaderBar } from './components/HeaderBar';
import { ItemConfigModal } from './components/ItemConfigModal';
import { QueueList } from './components/QueueList';
import { RecoveryBanner } from './components/RecoveryBanner';
import { SettingsModal } from './components/SettingsModal';
import { useBrowserKokoro } from './hooks/useBrowserKokoro';
import type {
  GlobalConfig,
  HardwareProfile,
  ItemConfig,
  JobItem,
  LogEntry,
  QueueSnapshot,
  RecoverableJob,
  ToastNotification,
  TTSHealthStatus,
} from './types/api';

export default function App() {
  const [queue, setQueue] = useState<QueueSnapshot>({
    status: 'idle',
    reuse_audio_chunks: true,
    items: [],
  });
  const [hardware, setHardware] = useState<HardwareProfile | null>(null);
  const [ttsStatus, setTTSStatus] = useState<TTSHealthStatus | null>(null);
  const [settings, setSettings] = useState<GlobalConfig | null>(null);
  const [recoveryJobs, setRecoveryJobs] = useState<RecoverableJob[]>([]);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [toasts, setToasts] = useState<ToastNotification[]>([]);

  const [checkingTTS, setCheckingTTS] = useState(false);
  const [browserOpen, setBrowserOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [editingJob, setEditingJob] = useState<JobItem | null>(null);
  const [quickPath, setQuickPath] = useState('');
  const [addingQuickPath, setAddingQuickPath] = useState(false);
  const [sseState, setSseState] = useState<'connected' | 'reconnecting' | 'paused'>('reconnecting');
  const browserKokoro = useBrowserKokoro();

  const pushToast = useCallback(
    (level: ToastNotification['level'], title: string, message: string) => {
      const id = `${Date.now()}-${Math.random()}`;
      setToasts((prev) => [...prev.slice(-4), { id, level, title, message }]);
      setTimeout(() => {
        setToasts((prev) => prev.filter((t) => t.id !== id));
      }, 5000);
    },
    []
  );

  const refreshQueue = useCallback(async () => {
    try {
      const snap = await api.getQueue();
      setQueue(snap);
    } catch {
      // ignore transient network errors
    }
  }, []);

  const refreshRecovery = useCallback(async () => {
    try {
      const res = await api.getRecovery();
      setRecoveryJobs(res.jobs || []);
    } catch {
      // ignore
    }
  }, []);

  const syncLogsAndNotifications = useCallback(async () => {
    try {
      const [dbLogs, unread] = await Promise.all([
        api.getLogs(),
        api.getUnreadNotifications(),
      ]);
      if (Array.isArray(dbLogs)) {
        setLogs(dbLogs);
      }
      if (Array.isArray(unread) && unread.length > 0) {
        let maxId = 0;
        for (const n of unread) {
          pushToast(n.level || 'info', n.title || 'Notice', n.message || '');
          if (n.id > maxId) maxId = n.id;
        }
        if (maxId > 0) {
          void api.markNotificationsRead(maxId);
        }
      }
    } catch {
      // ignore
    }
  }, [pushToast]);

  // Initial data load
  useEffect(() => {
    void refreshQueue();
    void refreshRecovery();
    void syncLogsAndNotifications();
    api.getHardware().then(setHardware).catch(() => {});
    api.getTTSStatus().then(setTTSStatus).catch(() => {});
    api.getSettings().then(setSettings).catch(() => {});
  }, [refreshQueue, refreshRecovery, syncLogsAndNotifications]);

  // Real-time SSE subscription with automatic pause/resume on tab visibility changes
  // (stays connected in background only when In-Browser KokoroJS is active to serve TTS requests)
  useEffect(() => {
    let es: EventSource | null = null;

    const connect = () => {
      if (es) return;
      setSseState('reconnecting');
      const source = new EventSource('/api/events');
      es = source;

      source.onopen = () => {
        setSseState('connected');
        void refreshQueue();
        void refreshRecovery();
        void syncLogsAndNotifications();
      };

      source.onerror = () => {
        setSseState('reconnecting');
      };

      source.addEventListener('tts_status', (e) => {
        try {
          setTTSStatus(JSON.parse((e as MessageEvent).data));
        } catch {
          // ignore
        }
      });

      source.addEventListener('queue_state', (e) => {
        try {
          const data = JSON.parse((e as MessageEvent).data);
          setQueue((prev) => ({
            ...prev,
            status: data.status ?? prev.status,
            active_job_id: data.active_job_id,
            single_target_id: data.single_target_id,
            reuse_audio_chunks:
              typeof data.reuse_audio_chunks === 'boolean'
                ? data.reuse_audio_chunks
                : prev.reuse_audio_chunks,
          }));
          void refreshRecovery();
        } catch {
          // ignore
        }
      });

      source.addEventListener('job_updated', (e) => {
        try {
          const updated: JobItem = JSON.parse((e as MessageEvent).data);
          let found = false;
          setQueue((prev) => {
            const nextItems = prev.items.map((it) => {
              if (it.id === updated.id) {
                found = true;
                return updated;
              }
              return it;
            });
            return { ...prev, items: nextItems };
          });
          if (!found) {
            void refreshQueue();
          }
          if (updated.status === 'completed' || updated.status === 'paused') {
            void refreshRecovery();
          }
        } catch {
          // ignore
        }
      });

      source.addEventListener('job_list_changed', () => {
        void refreshQueue();
      });

      source.addEventListener('log_entry', (e) => {
        try {
          const entry: LogEntry = JSON.parse((e as MessageEvent).data);
          setLogs((prev) => {
            const key = `${entry.timestamp}|${entry.job_id}|${entry.message}`;
            if (prev.some((p) => `${p.timestamp}|${p.job_id}|${p.message}` === key)) {
              return prev;
            }
            return [entry, ...prev].slice(0, 100);
          });
        } catch {
          // ignore
        }
      });

      source.addEventListener('notification', (e) => {
        try {
          const n = JSON.parse((e as MessageEvent).data);
          pushToast(n.level || 'info', n.title || 'Notice', n.message || '');
          if (typeof n.id === 'number' && n.id > 0) {
            void api.markNotificationsRead(n.id);
          }
        } catch {
          // ignore
        }
      });

      source.addEventListener('browser_tts_request', (e) => {
        try {
          const req = JSON.parse((e as MessageEvent).data);
          browserKokoro.handleTTSRequest(req);
        } catch {
          // ignore
        }
      });
    };

    const disconnect = () => {
      if (es) {
        es.close();
        es = null;
      }
      setSseState('paused');
    };

    const handleVisibilityChange = () => {
      if (document.hidden) {
        // Keep SSE open in background only if Browser KokoroJS is actively serving English TTS
        if (browserKokoro.state !== 'ready') {
          disconnect();
        }
      } else {
        connect();
      }
    };

    if (!document.hidden || browserKokoro.state === 'ready') {
      connect();
    } else {
      setSseState('paused');
    }

    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      if (es) {
        es.close();
        es = null;
      }
    };
  }, [
    pushToast,
    refreshQueue,
    refreshRecovery,
    syncLogsAndNotifications,
    browserKokoro.handleTTSRequest,
    browserKokoro.state,
  ]);

  const handleCheckTTS = async () => {
    setCheckingTTS(true);
    try {
      const st = await api.checkTTSNow();
      setTTSStatus(st);
    } finally {
      setCheckingTTS(false);
    }
  };

  const handleAddPaths = async (paths: string[]) => {
    const res = await api.addQueueItems(paths);
    await refreshQueue();
    pushToast('success', 'Added to Queue', `Queued ${res.count} video(s).`);
  };

  const handleQuickPathSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = quickPath.trim();
    if (!trimmed) return;
    setAddingQuickPath(true);
    try {
      await handleAddPaths([trimmed]);
      setQuickPath('');
    } catch (err) {
      pushToast('error', 'Failed to Add Path', err instanceof Error ? err.message : 'Unknown error');
    } finally {
      setAddingQuickPath(false);
    }
  };

  const handleStart = async () => {
    try {
      const snap = await api.startQueue(queue.reuse_audio_chunks);
      setQueue(snap);
    } catch (err) {
      pushToast('error', 'Failed to Start', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleStartSingle = async (id: string) => {
    try {
      const snap = await api.startSingleItem(id, queue.reuse_audio_chunks);
      setQueue(snap);
    } catch (err) {
      pushToast('error', 'Failed to Start Item', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleClearCompleted = async () => {
    try {
      const res = await api.clearCompletedQueue();
      setQueue(res.queue);
      if (res.removed_count > 0) {
        pushToast('info', 'Cleared Completed', `Removed ${res.removed_count} completed task(s).`);
      }
    } catch (err) {
      pushToast('error', 'Clear Failed', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handlePause = async () => {
    try {
      const snap = await api.pauseQueue();
      setQueue(snap);
    } catch (err) {
      pushToast('error', 'Failed to Pause', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleStop = async () => {
    try {
      const snap = await api.stopQueue();
      setQueue(snap);
    } catch (err) {
      pushToast('error', 'Failed to Stop', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleToggleReuseChunks = async (val: boolean) => {
    const snap = await api.setReuseChunks(val);
    setQueue(snap);
  };

  const handleRemoveItem = async (id: string) => {
    try {
      await api.deleteQueueItem(id);
      await refreshQueue();
    } catch (err) {
      pushToast('error', 'Remove Failed', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleMoveItem = async (index: number, dir: -1 | 1) => {
    const target = index + dir;
    if (target < 0 || target >= queue.items.length) return;
    const ids = queue.items.map((it) => it.id);
    [ids[index], ids[target]] = [ids[target], ids[index]];
    const snap = await api.reorderQueue(ids);
    setQueue(snap);
  };

  const handleSaveItemConfig = async (jobId: string, cfg: ItemConfig) => {
    await api.updateQueueItem(jobId, cfg);
    await refreshQueue();
    pushToast('success', 'Job Updated', 'Per-item dubbing & dual-subtitle config saved.');
  };

  const handleToggleLanguage = async (job: JobItem, lang: 'en' | 'vi', checked: boolean) => {
    const langs = new Set(job.config.dub_languages || []);
    if (checked) langs.add(lang);
    else langs.delete(lang);
    try {
      await api.updateQueueItem(job.id, { ...job.config, dub_languages: Array.from(langs) });
      await refreshQueue();
    } catch (err) {
      pushToast('error', 'Update Failed', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleUploadSubtitle = async (
    jobId: string,
    file: File,
    lang: 'en' | 'vi',
    role: 'both' | 'tts' | 'embed'
  ) => {
    try {
      await api.uploadQueueSubtitle(jobId, file, lang, role);
      await refreshQueue();
    } catch (err) {
      pushToast('error', 'Upload Failed', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleResumeRecovery = async (jobId: string) => {
    try {
      await api.resumeRecovery(jobId);
      await Promise.all([refreshQueue(), refreshRecovery()]);
      pushToast('success', 'Job Restored', 'Added unfinished job to top of queue.');
    } catch (err) {
      pushToast('error', 'Recovery Failed', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleDiscardRecovery = async (jobId: string) => {
    try {
      await api.discardRecovery(jobId);
      await refreshRecovery();
    } catch (err) {
      pushToast('error', 'Discard Failed', err instanceof Error ? err.message : 'Unknown error');
    }
  };

  const handleSaveSettings = async (cfg: GlobalConfig) => {
    const saved = await api.updateSettings(cfg);
    setSettings(saved);
  };

  const hasPendingJobs = queue.items.some(
    (it) => it.status === 'pending' || it.status === 'paused'
  );
  const completedCount = queue.items.filter((it) => it.status === 'completed').length;

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <HeaderBar
        runnerStatus={queue.status}
        singleTargetId={queue.single_target_id}
        reuseAudioChunks={queue.reuse_audio_chunks}
        ttsStatus={ttsStatus}
        browserKokoroState={browserKokoro.state}
        browserKokoroProgress={browserKokoro.progress}
        browserKokoroDevice={browserKokoro.device}
        browserKokoroError={browserKokoro.error}
        hardware={hardware}
        checkingTTS={checkingTTS}
        hasPendingJobs={hasPendingJobs}
        onToggleReuseChunks={(v) => void handleToggleReuseChunks(v)}
        onToggleBrowserKokoro={browserKokoro.toggle}
        onStart={() => void handleStart()}
        onPause={() => void handlePause()}
        onStop={() => void handleStop()}
        onOpenBrowser={() => setBrowserOpen(true)}
        onOpenSettings={() => setSettingsOpen(true)}
        onCheckTTS={() => void handleCheckTTS()}
      />

      <main className="mx-auto max-w-7xl space-y-6 px-4 py-6 sm:px-6">
        <RecoveryBanner
          jobs={recoveryJobs}
          onResume={(id) => void handleResumeRecovery(id)}
          onDiscard={(id) => void handleDiscardRecovery(id)}
        />

        <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
          {/* Queue Column */}
          <section className="space-y-3 lg:col-span-2">
            <div className="flex items-center justify-between gap-2">
              <h2 className="text-sm font-semibold uppercase tracking-wider text-slate-400">
                Dubbing Queue ({queue.items.length})
              </h2>
              <div className="flex items-center gap-3">
                {completedCount > 0 && (
                  <button
                    type="button"
                    onClick={() => void handleClearCompleted()}
                    className="inline-flex items-center gap-1 rounded-lg border border-slate-800 bg-slate-900 px-2.5 py-1 text-xs font-medium text-emerald-300 transition hover:border-emerald-500/40 hover:bg-emerald-500/10"
                  >
                    <CheckCheck className="h-3.5 w-3.5" />
                    Clear Completed ({completedCount})
                  </button>
                )}
                <span className="text-xs capitalize text-slate-500">Status: {queue.status}</span>
              </div>
            </div>

            {/* Quick Paste Path Bar (macOS Copy as Pathname) */}
            <form
              onSubmit={(e) => void handleQuickPathSubmit(e)}
              className="flex items-center gap-2 rounded-xl border border-slate-800 bg-slate-900/70 p-2"
            >
              <ClipboardPaste className="ml-1.5 h-4 w-4 shrink-0 text-slate-500" />
              <input
                type="text"
                value={quickPath}
                onChange={(e) => setQuickPath(e.target.value)}
                placeholder="Paste a file or folder path (e.g. '/Users/.../Downloads/Episode.mkv') and press Enter..."
                className="flex-1 bg-transparent px-1 py-1 font-mono text-xs text-slate-200 placeholder:text-slate-500 focus:outline-none"
              />
              <button
                type="submit"
                disabled={!quickPath.trim() || addingQuickPath}
                className="inline-flex shrink-0 items-center gap-1 rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-semibold text-white transition hover:bg-indigo-500 disabled:opacity-40"
              >
                <Plus className="h-3.5 w-3.5" />
                Add
              </button>
            </form>

            <QueueList
              items={queue.items}
              activeJobId={queue.active_job_id}
              runnerRunning={queue.status === 'running' || queue.status === 'pausing' || queue.status === 'stopping'}
              onStartSingle={(id) => void handleStartSingle(id)}
              onEdit={setEditingJob}
              onRemove={(id) => void handleRemoveItem(id)}
              onMove={(idx, dir) => void handleMoveItem(idx, dir)}
              onToggleLanguage={handleToggleLanguage}
              onUploadSubtitle={handleUploadSubtitle}
            />
          </section>

          {/* Live Activity Log Column */}
          <aside className="space-y-3">
            <div className="flex items-center justify-between">
              <h2 className="inline-flex items-center gap-1.5 text-sm font-semibold uppercase tracking-wider text-slate-400">
                <Terminal className="h-4 w-4 text-indigo-400" />
                Live Activity (SSE)
                <span
                  title={
                    sseState === 'connected'
                      ? 'SSE Connected'
                      : sseState === 'paused'
                      ? 'SSE Paused (background tab)'
                      : 'SSE Reconnecting...'
                  }
                  className={`ml-1 inline-block h-2 w-2 rounded-full ${
                    sseState === 'connected'
                      ? 'bg-emerald-400'
                      : sseState === 'paused'
                      ? 'bg-slate-500'
                      : 'animate-pulse bg-amber-400'
                  }`}
                />
              </h2>
              {logs.length > 0 && (
                <button
                  type="button"
                  onClick={() => {
                    setLogs([]);
                    void api.clearLogs();
                  }}
                  className="text-xs text-slate-500 hover:text-slate-300"
                >
                  Clear
                </button>
              )}
            </div>

            <div className="h-[520px] overflow-y-auto rounded-xl border border-slate-800 bg-slate-900/60 p-3 font-mono text-xs">
              {logs.length === 0 ? (
                <div className="py-12 text-center text-slate-500">
                  Listening for real-time server events...
                </div>
              ) : (
                <div className="space-y-1.5">
                  {logs.map((entry, idx) => (
                    <div key={`${entry.timestamp}-${idx}`} className="leading-relaxed">
                      <span className="text-slate-600">
                        [{new Date(entry.timestamp).toLocaleTimeString()}]
                      </span>{' '}
                      <span
                        className={
                          entry.level === 'error'
                            ? 'text-rose-400'
                            : entry.level === 'warn'
                            ? 'text-amber-300'
                            : 'text-slate-300'
                        }
                      >
                        {entry.message}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </aside>
        </div>
      </main>

      {/* Modals */}
      <FileBrowserModal
        open={browserOpen}
        initialPath={settings?.nas_dir}
        onClose={() => setBrowserOpen(false)}
        onAddPaths={handleAddPaths}
      />

      <ItemConfigModal
        job={editingJob}
        geminiReady={Boolean(settings?.gemini_enabled && settings?.gemini_api_key)}
        onClose={() => setEditingJob(null)}
        onSave={handleSaveItemConfig}
        onJobUpdated={refreshQueue}
      />

      <SettingsModal
        open={settingsOpen}
        config={settings}
        hardware={hardware}
        onClose={() => setSettingsOpen(false)}
        onSave={handleSaveSettings}
      />

      {/* Toast Notifications */}
      <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={`flex max-w-sm items-start gap-3 rounded-xl border px-4 py-3 text-xs shadow-xl backdrop-blur-md ${
              t.level === 'error'
                ? 'border-rose-500/40 bg-rose-950/90 text-rose-100'
                : t.level === 'success'
                ? 'border-emerald-500/40 bg-emerald-950/90 text-emerald-100'
                : 'border-slate-700 bg-slate-900/95 text-slate-100'
            }`}
          >
            <div className="flex-1">
              <div className="font-semibold">{t.title}</div>
              <div className="mt-0.5 opacity-85">{t.message}</div>
            </div>
            <button
              type="button"
              onClick={() => setToasts((prev) => prev.filter((x) => x.id !== t.id))}
              className="opacity-60 hover:opacity-100"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}
