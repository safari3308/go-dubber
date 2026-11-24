import { useEffect, useState } from 'react';
import { Cpu, Loader2, Save, X } from 'lucide-react';
import type { GlobalConfig, HardwareProfile } from '../types/api';

interface SettingsModalProps {
  open: boolean;
  config: GlobalConfig | null;
  hardware: HardwareProfile | null;
  onClose: () => void;
  onSave: (cfg: GlobalConfig) => Promise<void>;
}

export function SettingsModal({
  open,
  config,
  hardware,
  onClose,
  onSave,
}: SettingsModalProps) {
  const [draft, setDraft] = useState<GlobalConfig | null>(null);
  const [langsInput, setLangsInput] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (open && config) {
      setDraft(structuredClone(config));
      setLangsInput((config.dub_languages || []).join(', '));
      setError('');
    }
  }, [open, config]);

  if (!open || !draft) return null;

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const langs = langsInput
        .split(',')
        .map((l) => l.trim().toLowerCase())
        .filter(Boolean);
      await onSave({ ...draft, dub_languages: langs.length ? langs : ['vi'] });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
      <div className="flex max-h-[88vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-slate-800 bg-slate-900 shadow-2xl">
        <div className="flex items-center justify-between border-b border-slate-800 px-5 py-4">
          <h3 className="text-base font-semibold text-white">Global Settings & Hardware</h3>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-1.5 text-slate-400 hover:bg-slate-800 hover:text-white"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 space-y-5 overflow-y-auto p-5">
          {error && (
            <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
              {error}
            </div>
          )}

          {/* Hardware Detection Card */}
          {hardware && (
            <div className="rounded-xl border border-sky-500/20 bg-sky-500/5 p-4">
              <div className="flex items-center gap-2 text-xs font-semibold text-sky-300">
                <Cpu className="h-4 w-4" />
                Auto-Detected Machine Profile
              </div>
              <div className="mt-2 grid grid-cols-2 gap-2 text-xs text-slate-300 sm:grid-cols-3">
                <div>
                  <span className="text-slate-500">OS / Arch:</span> {hardware.os}/{hardware.arch}
                </div>
                <div>
                  <span className="text-slate-500">CPU Cores:</span> {hardware.cpu_cores}
                </div>
                <div>
                  <span className="text-slate-500">Selected:</span> {hardware.selected_encoder}
                </div>
                <div className="col-span-2 sm:col-span-3">
                  <span className="text-slate-500">Verified Encoders:</span>{' '}
                  {hardware.available_encoders?.join(', ') || 'none'}
                </div>
              </div>
            </div>
          )}

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="sm:col-span-2">
              <label className="block text-xs font-medium text-slate-300">
                Default NAS / Media Root Directory
              </label>
              <input
                type="text"
                value={draft.nas_dir}
                onChange={(e) => setDraft({ ...draft, nas_dir: e.target.value })}
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">TTS Service URL</label>
              <input
                type="text"
                value={draft.api_url}
                onChange={(e) => setDraft({ ...draft, api_url: e.target.value })}
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">TTS API Token</label>
              <input
                type="password"
                value={draft.api_token}
                onChange={(e) => setDraft({ ...draft, api_token: e.target.value })}
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">
                Default Dub Languages
              </label>
              <input
                type="text"
                value={langsInput}
                onChange={(e) => setLangsInput(e.target.value)}
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">
                TTS Workers / Speed
              </label>
              <div className="mt-1 flex gap-2">
                <input
                  type="number"
                  min={1}
                  max={16}
                  value={draft.workers}
                  onChange={(e) =>
                    setDraft({ ...draft, workers: parseInt(e.target.value, 10) || 2 })
                  }
                  className="w-1/2 rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
                />
                <input
                  type="number"
                  step={0.05}
                  min={0.5}
                  max={2.0}
                  value={draft.tts_speed}
                  onChange={(e) =>
                    setDraft({ ...draft, tts_speed: parseFloat(e.target.value) || 1.1 })
                  }
                  className="w-1/2 rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
                />
              </div>
            </div>

            <div className="sm:col-span-2 grid grid-cols-2 gap-3">
              {[
                { key: 'auto_detect_hardware', label: 'Auto-Detect FFmpeg Hardware' },
                { key: 'use_gpu', label: 'Prefer GPU Encoding' },
                { key: 'skip_encode', label: 'Skip Video Re-encode by Default' },
                { key: 'drop_song_subtitles', label: 'Drop Song/Karaoke Subtitles' },
              ].map(({ key, label }) => (
                <label
                  key={key}
                  className="flex cursor-pointer items-center gap-2 rounded-lg border border-slate-800 bg-slate-950/60 px-3 py-2 text-xs text-slate-300"
                >
                  <input
                    type="checkbox"
                    checked={Boolean(draft[key as keyof GlobalConfig])}
                    onChange={(e) => setDraft({ ...draft, [key]: e.target.checked })}
                    className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-indigo-500"
                  />
                  <span>{label}</span>
                </label>
              ))}
            </div>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-slate-800 bg-slate-950/60 px-5 py-3.5">
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg px-3 py-2 text-xs font-medium text-slate-400 hover:text-slate-200"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={saving}
            onClick={() => void handleSave()}
            className="inline-flex items-center gap-1.5 rounded-lg bg-indigo-600 px-4 py-2 text-xs font-semibold text-white hover:bg-indigo-500 disabled:opacity-40"
          >
            {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
            Save Settings
          </button>
        </div>
      </div>
    </div>
  );
}
