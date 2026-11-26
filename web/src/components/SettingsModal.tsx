import { useEffect, useState } from 'react';
import { ChevronDown, ChevronRight, Cpu, Database, Loader2, Save, Sparkles, X } from 'lucide-react';
import type { GlobalConfig, HardwareProfile } from '../types/api';

interface SettingsModalProps {
  open: boolean;
  config: GlobalConfig | null;
  hardware: HardwareProfile | null;
  onClose: () => void;
  onSave: (cfg: GlobalConfig) => Promise<void>;
}

const SUPPORTED_LANGS: { code: 'en' | 'vi'; label: string }[] = [
  { code: 'en', label: 'English (EN)' },
  { code: 'vi', label: 'Vietnamese (VI)' },
];

export function SettingsModal({
  open,
  config,
  hardware,
  onClose,
  onSave,
}: SettingsModalProps) {
  const [draft, setDraft] = useState<GlobalConfig | null>(null);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (open && config) {
      setDraft(structuredClone(config));
      setShowAdvanced(false);
      setError('');
    }
  }, [open, config]);

  if (!open || !draft) return null;

  const toggleDefaultLang = (code: 'en' | 'vi', checked: boolean) => {
    const set = new Set(draft.dub_languages || []);
    if (checked) set.add(code);
    else set.delete(code);
    setDraft({ ...draft, dub_languages: Array.from(set) });
  };

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const langs = (draft.dub_languages || []).filter((l) => l === 'en' || l === 'vi');
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
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-800 px-5 py-4">
          <div>
            <h3 className="text-base font-semibold text-white">Settings</h3>
            <p className="mt-0.5 inline-flex items-center gap-1.5 text-xs text-slate-400">
              <Database className="h-3 w-3 text-indigo-400" />
              Stored locally in SQLite (workdir/dubber.db) — no config.json required
            </p>
          </div>
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

          {/* Basic Settings */}
          <div className="space-y-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div>
                <label className="block text-xs font-medium text-slate-300">TTS HTTP URL</label>
                <input
                  type="text"
                  value={draft.api_url}
                  onChange={(e) => setDraft({ ...draft, api_url: e.target.value })}
                  placeholder="http://localhost:8000"
                  className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
                />
                <p className="mt-1 text-[11px] text-slate-500">
                  Fallback HTTP endpoint (also optional for EN if using Browser KokoroJS).
                </p>
              </div>

              <div>
                <label className="block text-xs font-medium text-slate-300">
                  TTS gRPC Address <span className="text-slate-500">(optional)</span>
                </label>
                <input
                  type="text"
                  value={draft.grpc_url || ''}
                  onChange={(e) => setDraft({ ...draft, grpc_url: e.target.value })}
                  placeholder="Auto (<host>:50051)"
                  className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
                />
                <p className="mt-1 text-[11px] text-slate-500">
                  Preferred when available; auto-falls back to HTTP. Use &quot;disabled&quot; for HTTP-only.
                </p>
              </div>
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">
                TTS API Token <span className="text-slate-500">(optional)</span>
              </label>
              <input
                type="password"
                value={draft.api_token}
                onChange={(e) => setDraft({ ...draft, api_token: e.target.value })}
                placeholder="Bearer token (shared by gRPC & HTTP)"
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">
                Default Media / NAS Folder
              </label>
              <input
                type="text"
                value={draft.nas_dir}
                onChange={(e) => setDraft({ ...draft, nas_dir: e.target.value })}
                placeholder="/Volumes/Media or /Users/.../Downloads"
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 font-mono text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div>
                <label className="block text-xs font-medium text-slate-300">
                  Default Dub Languages
                </label>
                <div className="mt-1.5 flex gap-2">
                  {SUPPORTED_LANGS.map(({ code, label }) => {
                    const checked = (draft.dub_languages || []).includes(code);
                    return (
                      <label
                        key={code}
                        className={`flex flex-1 cursor-pointer items-center justify-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium transition ${
                          checked
                            ? 'border-indigo-500/60 bg-indigo-500/15 text-indigo-200'
                            : 'border-slate-700 bg-slate-950 text-slate-400 hover:border-slate-600'
                        }`}
                      >
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={(e) => toggleDefaultLang(code, e.target.checked)}
                          className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-indigo-500"
                        />
                        <span>{label}</span>
                      </label>
                    );
                  })}
                </div>
              </div>

              <div>
                <label className="block text-xs font-medium text-slate-300">
                  Default TTS Speed ({draft.tts_speed.toFixed(2)}x)
                </label>
                <input
                  type="number"
                  step={0.05}
                  min={0.5}
                  max={2.0}
                  value={draft.tts_speed}
                  onChange={(e) =>
                    setDraft({ ...draft, tts_speed: parseFloat(e.target.value) || 1.1 })
                  }
                  className="mt-1.5 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
                />
              </div>
            </div>

            {/* Google AI (Gemini) Toggleable Subtitle Integration */}
            <div className="rounded-xl border border-violet-500/20 bg-violet-500/5 p-3.5">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 text-xs font-semibold text-violet-300">
                  <Sparkles className="h-4 w-4" />
                  Google AI (Gemini) Subtitle Processing
                </div>
                <label className="inline-flex cursor-pointer items-center gap-2 text-xs text-slate-300">
                  <span>{draft.gemini_enabled ? 'Enabled' : 'Disabled'}</span>
                  <input
                    type="checkbox"
                    checked={Boolean(draft.gemini_enabled)}
                    onChange={(e) => setDraft({ ...draft, gemini_enabled: e.target.checked })}
                    className="h-4 w-4 rounded border-slate-600 bg-slate-900 text-violet-500"
                  />
                </label>
              </div>

              {draft.gemini_enabled && (
                <div className="mt-3 space-y-3">
                  <div>
                    <label className="block text-[11px] font-medium text-slate-300">
                      Google AI API Key
                    </label>
                    <input
                      type="password"
                      value={draft.gemini_api_key || ''}
                      onChange={(e) => setDraft({ ...draft, gemini_api_key: e.target.value })}
                      placeholder="AIza..."
                      className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-1.5 font-mono text-xs text-slate-100 focus:border-violet-500 focus:outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[11px] font-medium text-slate-300">
                      Default Translation / Cleanup Style
                    </label>
                    <select
                      value={draft.gemini_style || 'standard'}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          gemini_style: e.target.value as 'standard' | 'donghua',
                        })
                      }
                      className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-1.5 text-xs text-slate-100 focus:border-violet-500 focus:outline-none"
                    >
                      <option value="standard">Standard (Modern / Conversational)</option>
                      <option value="donghua">
                        Donghua / Xianxia (Classical Hán Việt, strips TN/credits/signs)
                      </option>
                    </select>
                  </div>
                  <div className="flex flex-wrap gap-3">
                    <label className="flex cursor-pointer items-center gap-2 text-xs text-slate-300">
                      <input
                        type="checkbox"
                        checked={draft.gemini_clean_for_tts ?? true}
                        onChange={(e) =>
                          setDraft({ ...draft, gemini_clean_for_tts: e.target.checked })
                        }
                        className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-violet-500"
                      />
                      <span>Filter signs/descriptions for TTS</span>
                    </label>
                    <label className="flex cursor-pointer items-center gap-2 text-xs text-slate-300">
                      <input
                        type="checkbox"
                        checked={Boolean(draft.gemini_translate)}
                        onChange={(e) =>
                          setDraft({ ...draft, gemini_translate: e.target.checked })
                        }
                        className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-violet-500"
                      />
                      <span>Translate dialogue to target language</span>
                    </label>
                  </div>
                </div>
              )}
            </div>

            {/* Hardware Detection Card */}
            {hardware && (
              <div className="rounded-xl border border-sky-500/20 bg-sky-500/5 p-3.5">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex items-center gap-2 text-xs font-semibold text-sky-300">
                    <Cpu className="h-4 w-4" />
                    Auto-Detected Encoder: {hardware.encoder_label}
                  </div>
                  <span className="text-[11px] text-slate-400">
                    {hardware.os}/{hardware.arch} • {hardware.cpu_cores} cores
                  </span>
                </div>
                <div className="mt-3 flex flex-wrap gap-3">
                  <label className="flex cursor-pointer items-center gap-2 text-xs text-slate-300">
                    <input
                      type="checkbox"
                      checked={Boolean(draft.auto_detect_hardware)}
                      onChange={(e) =>
                        setDraft({ ...draft, auto_detect_hardware: e.target.checked })
                      }
                      className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-indigo-500"
                    />
                    <span>Auto-tune FFmpeg for this machine</span>
                  </label>
                  <label className="flex cursor-pointer items-center gap-2 text-xs text-slate-300">
                    <input
                      type="checkbox"
                      checked={Boolean(draft.skip_encode)}
                      onChange={(e) => setDraft({ ...draft, skip_encode: e.target.checked })}
                      className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-indigo-500"
                    />
                    <span>Skip video re-encoding (copy stream)</span>
                  </label>
                </div>
              </div>
            )}
          </div>

          {/* Collapsible Advanced Settings */}
          <div className="rounded-xl border border-slate-800 bg-slate-950/50">
            <button
              type="button"
              onClick={() => setShowAdvanced((v) => !v)}
              className="flex w-full items-center justify-between px-4 py-3 text-left text-xs font-semibold text-slate-300 hover:text-white"
            >
              <span>Advanced Settings (Workers, Audio Mix, Subtitle Sync &amp; FFmpeg)</span>
              {showAdvanced ? (
                <ChevronDown className="h-4 w-4 text-slate-400" />
              ) : (
                <ChevronRight className="h-4 w-4 text-slate-400" />
              )}
            </button>

            {showAdvanced && (
              <div className="space-y-4 border-t border-slate-800 p-4">
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                  <div>
                    <label className="block text-[11px] font-medium text-slate-400">
                      TTS Workers
                    </label>
                    <input
                      type="number"
                      min={1}
                      max={16}
                      value={draft.workers}
                      onChange={(e) =>
                        setDraft({ ...draft, workers: parseInt(e.target.value, 10) || 2 })
                      }
                      className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-900 px-2.5 py-1.5 text-xs text-slate-100"
                    />
                  </div>
                  <div>
                    <label className="block text-[11px] font-medium text-slate-400">
                      Volume Boost
                    </label>
                    <input
                      type="text"
                      value={draft.ffmpeg?.volume_boost || '2.0'}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          ffmpeg: { ...draft.ffmpeg, volume_boost: e.target.value },
                        })
                      }
                      className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-900 px-2.5 py-1.5 text-xs text-slate-100"
                    />
                  </div>
                  <div>
                    <label className="block text-[11px] font-medium text-slate-400">
                      Audio Bitrate
                    </label>
                    <input
                      type="text"
                      value={draft.ffmpeg?.audio_bitrate || '192k'}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          ffmpeg: { ...draft.ffmpeg, audio_bitrate: e.target.value },
                        })
                      }
                      className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-900 px-2.5 py-1.5 text-xs text-slate-100"
                    />
                  </div>
                </div>

                <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
                  {[
                    { key: 'use_gpu', label: 'Prefer GPU encoding' },
                    { key: 'skip_sub_sync', label: 'Skip subtitle sync by default' },
                    { key: 'drop_song_subtitles', label: 'Drop song / karaoke subtitles' },
                    { key: 'ai_track_as_first_track', label: 'Set AI audio as Track #0' },
                  ].map(({ key, label }) => (
                    <label
                      key={key}
                      className="flex cursor-pointer items-center gap-2 rounded-lg border border-slate-800 bg-slate-900/70 px-3 py-2 text-xs text-slate-300"
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
            )}
          </div>
        </div>

        {/* Footer */}
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
            {saving ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Save className="h-3.5 w-3.5" />
            )}
            Save Settings
          </button>
        </div>
      </div>
    </div>
  );
}
