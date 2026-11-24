import type {
  FSBrowseResponse,
  FSInspectResponse,
  GlobalConfig,
  HardwareProfile,
  ItemConfig,
  JobItem,
  QueueSnapshot,
  RecoverableJob,
  TTSHealthStatus,
} from '../types/api';

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { 'Content-Type': 'application/json', ...(init?.headers || {}) },
    ...init,
  });
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      // ignore
    }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}

export const api = {
  getHardware: () => request<HardwareProfile>('/api/system/hardware'),
  getTTSStatus: () => request<TTSHealthStatus>('/api/system/tts-status'),
  checkTTSNow: () => request<TTSHealthStatus>('/api/system/tts-check', { method: 'POST' }),

  getSettings: () => request<GlobalConfig>('/api/settings'),
  updateSettings: (cfg: GlobalConfig) =>
    request<GlobalConfig>('/api/settings', { method: 'PUT', body: JSON.stringify(cfg) }),

  browseFS: (path?: string) =>
    request<FSBrowseResponse>(`/api/fs/browse${path ? `?path=${encodeURIComponent(path)}` : ''}`),
  inspectVideo: (path: string, languages?: string[]) =>
    request<FSInspectResponse>('/api/fs/inspect', {
      method: 'POST',
      body: JSON.stringify({ path, languages }),
    }),
  extractSubtitleJSON: (path: string, subIndex: number, format: 'original' | 'srt' = 'original') =>
    request<{ filename: string; format: string; content: string }>('/api/fs/extract-subtitle', {
      method: 'POST',
      body: JSON.stringify({ path, sub_index: subIndex, format, as_json: true }),
    }),
  uploadQueueSubtitle: async (
    jobId: string,
    file: File,
    language: 'en' | 'vi',
    role: 'both' | 'tts' | 'embed' = 'both'
  ): Promise<JobItem> => {
    const form = new FormData();
    form.append('file', file);
    form.append('language', language);
    form.append('role', role);
    const res = await fetch(`/api/queue/items/${encodeURIComponent(jobId)}/subtitles`, {
      method: 'POST',
      body: form,
    });
    if (!res.ok) {
      let msg = `HTTP ${res.status}`;
      try {
        const body = await res.json();
        if (body?.error) msg = body.error;
      } catch {
        // ignore
      }
      throw new Error(msg);
    }
    return res.json() as Promise<JobItem>;
  },

  getQueue: () => request<QueueSnapshot>('/api/queue'),
  addQueueItems: (paths: string[], config?: Partial<ItemConfig>) =>
    request<{ added: JobItem[]; count: number }>('/api/queue/items', {
      method: 'POST',
      body: JSON.stringify({ paths, config }),
    }),
  updateQueueItem: (id: string, config: ItemConfig) =>
    request<JobItem>(`/api/queue/items/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(config),
    }),
  deleteQueueItem: (id: string) =>
    request<{ removed: string }>(`/api/queue/items/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
  reorderQueue: (ids: string[]) =>
    request<QueueSnapshot>('/api/queue/reorder', {
      method: 'POST',
      body: JSON.stringify({ ids }),
    }),

  startQueue: (reuseAudioChunks?: boolean) =>
    request<QueueSnapshot>('/api/queue/start', {
      method: 'POST',
      body: JSON.stringify(
        reuseAudioChunks === undefined ? {} : { reuse_audio_chunks: reuseAudioChunks }
      ),
    }),
  pauseQueue: () => request<QueueSnapshot>('/api/queue/pause', { method: 'POST' }),
  stopQueue: () => request<QueueSnapshot>('/api/queue/stop', { method: 'POST' }),
  setReuseChunks: (reuseAudioChunks: boolean) =>
    request<QueueSnapshot>('/api/queue/options', {
      method: 'PATCH',
      body: JSON.stringify({ reuse_audio_chunks: reuseAudioChunks }),
    }),

  getRecovery: () => request<{ jobs: RecoverableJob[]; count: number }>('/api/recovery'),
  resumeRecovery: (id: string) =>
    request<JobItem>(`/api/recovery/${encodeURIComponent(id)}/resume`, { method: 'POST' }),
  discardRecovery: (id: string) =>
    request<{ discarded: string }>(`/api/recovery/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
};

export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let idx = 0;
  let val = bytes;
  while (val >= 1024 && idx < units.length - 1) {
    val /= 1024;
    idx++;
  }
  return `${val.toFixed(idx === 0 ? 0 : 2)} ${units[idx]}`;
}

export function formatDuration(sec: number): string {
  if (!sec || sec <= 0) return '0s';
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  if (h > 0) return `${h}h ${m}m ${s}s`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}
