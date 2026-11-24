import { useEffect, useState } from 'react';
import { Loader2, Save, Subtitles, Volume2, X } from 'lucide-react';
import { api } from '../api/client';
import type {
  CandidateSubtitle,
  EmbeddedSubInfo,
  ItemConfig,
  JobItem,
  LanguageDubSpec,
  SubtitleSource,
} from '../types/api';

interface ItemConfigModalProps {
  job: JobItem | null;
  onClose: () => void;
  onSave: (jobId: string, cfg: ItemConfig) => Promise<void>;
}

export function ItemConfigModal({ job, onClose, onSave }: ItemConfigModalProps) {
  const [cfg, setCfg] = useState<ItemConfig | null>(null);
  const [langsInput, setLangsInput] = useState('');
  const [embeddedSubs, setEmbeddedSubs] = useState<EmbeddedSubInfo[]>([]);
  const [extCandidates, setExtCandidates] = useState<Record<string, CandidateSubtitle[]>>({});
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!job) return;
    setCfg(structuredClone(job.config));
    setLangsInput(job.config.dub_languages.join(', '));
    setError('');
    setLoading(true);
    api
      .inspectVideo(job.source_path, job.config.dub_languages)
      .then((res) => {
        setEmbeddedSubs(res.video_info.embedded_sub_streams || []);
        setExtCandidates(res.external_subtitles || {});
        setCfg((prev) => {
          if (!prev) return prev;
          if (!prev.language_specs || prev.language_specs.length === 0) {
            return { ...prev, language_specs: res.resolved_specs };
          }
          return prev;
        });
      })
      .catch(() => {
        // Fallback to cached video_info if inspect fails
        setEmbeddedSubs(job.video_info?.embedded_sub_streams || []);
      })
      .finally(() => setLoading(false));
  }, [job]);

  if (!job || !cfg) return null;

  const updateSpec = (lang: string, updater: (spec: LanguageDubSpec) => LanguageDubSpec) => {
    setCfg((prev) => {
      if (!prev) return prev;
      const specs = [...(prev.language_specs || [])];
      const idx = specs.findIndex((s) => s.language === lang);
      if (idx >= 0) {
        specs[idx] = updater(specs[idx]);
      } else {
        specs.push(
          updater({
            language: lang,
            tts_sub: { type: 'none', embedded_index: 0, format: '', description: '' },
            embed_sub: null,
            sync_tts_sub: !prev.skip_sub_sync,
            sync_embed_sub: false,
          })
        );
      }
      return { ...prev, language_specs: specs };
    });
  };

  const getSpec = (lang: string): LanguageDubSpec => {
    return (
      cfg.language_specs?.find((s) => s.language === lang) || {
        language: lang,
        tts_sub: { type: 'none', embedded_index: 0, format: '', description: '' },
        embed_sub: null,
        sync_tts_sub: !cfg.skip_sub_sync,
        sync_embed_sub: false,
      }
    );
  };

  const encodeSourceValue = (src?: SubtitleSource | null): string => {
    if (!src || src.type === 'none') return 'none';
    if (src.type === 'embedded') return `embedded:${src.embedded_index}`;
    return `external:${src.path || ''}`;
  };

  const decodeSourceValue = (val: string, lang: string): SubtitleSource => {
    if (val === 'none') {
      return { type: 'none', embedded_index: 0, format: '', description: 'None' };
    }
    if (val.startsWith('embedded:')) {
      const idx = parseInt(val.slice('embedded:'.length), 10) || 0;
      const emb = embeddedSubs.find((s) => s.sub_index === idx);
      return {
        type: 'embedded',
        embedded_index: idx,
        format: emb?.codec_name || 'srt',
        description: `Embedded #${idx} (${emb?.language || 'unknown'})`,
      };
    }
    const p = val.slice('external:'.length);
    const cand = (extCandidates[lang] || []).find((c) => c.path === p);
    const ext = p.split('.').pop()?.toLowerCase() || 'srt';
    return {
      type: 'external',
      path: p,
      embedded_index: 0,
      format: cand?.format || ext,
      description: cand?.file_name || p.split(/[/\\]/).pop() || p,
    };
  };

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const langs = langsInput
        .split(',')
        .map((l) => l.trim().toLowerCase())
        .filter(Boolean);
      const nextCfg: ItemConfig = {
        ...cfg,
        dub_languages: langs.length > 0 ? langs : ['vi'],
      };
      await onSave(job.id, nextCfg);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save config');
    } finally {
      setSaving(false);
    }
  };

  const activeLangs = langsInput
    .split(',')
    .map((l) => l.trim().toLowerCase())
    .filter(Boolean);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
      <div className="flex max-h-[88vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-slate-800 bg-slate-900 shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-800 px-5 py-4">
          <div className="min-w-0 pr-4">
            <h3 className="truncate text-base font-semibold text-white">
              Configure Job: {job.file_name}
            </h3>
            <p className="truncate text-xs text-slate-400">{job.source_path}</p>
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

          {/* General Per-Item Settings */}
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label className="block text-xs font-medium text-slate-300">
                Dub Languages (comma-separated)
              </label>
              <input
                type="text"
                value={langsInput}
                onChange={(e) => setLangsInput(e.target.value)}
                placeholder="vi, en"
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">
                Original Audio Language / Fallback Track
              </label>
              <div className="mt-1 flex gap-2">
                <input
                  type="text"
                  value={cfg.original_language}
                  onChange={(e) => setCfg({ ...cfg, original_language: e.target.value })}
                  placeholder="ja, en..."
                  className="w-2/3 rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
                />
                <input
                  type="number"
                  min={0}
                  value={cfg.original_audio_index}
                  onChange={(e) =>
                    setCfg({ ...cfg, original_audio_index: parseInt(e.target.value, 10) || 0 })
                  }
                  title="Fallback audio stream index"
                  className="w-1/3 rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
                />
              </div>
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">
                TTS Speed ({cfg.tts_speed.toFixed(2)}x)
              </label>
              <input
                type="number"
                step={0.05}
                min={0.5}
                max={2.0}
                value={cfg.tts_speed}
                onChange={(e) => setCfg({ ...cfg, tts_speed: parseFloat(e.target.value) || 1.1 })}
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">
                Volume Boost (TTS mix gain)
              </label>
              <input
                type="text"
                value={cfg.volume_boost}
                onChange={(e) => setCfg({ ...cfg, volume_boost: e.target.value })}
                placeholder="2.0"
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>
          </div>

          {/* Toggles */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            {[
              { key: 'use_gpu', label: 'Use GPU Encoding' },
              { key: 'skip_encode', label: 'Skip Video Re-encode' },
              { key: 'skip_sub_sync', label: 'Skip Subtitle Sync' },
              { key: 'force_reprocess', label: 'Force Reprocess' },
              { key: 'ai_track_as_first_track', label: 'AI Audio as Track #0' },
              { key: 'drop_song_subtitles', label: 'Drop Song/Karaoke Subs' },
            ].map(({ key, label }) => (
              <label
                key={key}
                className="flex cursor-pointer items-center gap-2 rounded-lg border border-slate-800 bg-slate-950/60 px-3 py-2 text-xs text-slate-300"
              >
                <input
                  type="checkbox"
                  checked={Boolean(cfg[key as keyof ItemConfig])}
                  onChange={(e) => setCfg({ ...cfg, [key]: e.target.checked })}
                  className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-indigo-500 focus:ring-indigo-500"
                />
                <span>{label}</span>
              </label>
            ))}
          </div>

          {/* Dual-Subtitle Configuration per Language */}
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Subtitles className="h-4 w-4 text-indigo-400" />
                <h4 className="text-sm font-semibold text-white">
                  Dual-Subtitle Mapping (TTS Voice vs. Embedded Video Subtitle)
                </h4>
              </div>
              {loading && <Loader2 className="h-4 w-4 animate-spin text-slate-400" />}
            </div>
            <p className="text-xs text-slate-400">
              Choose a clean subtitle (e.g., SRT) for AI voice synthesis and optionally a separate
              styled subtitle (e.g., ASS with signs & descriptions) to embed into the MKV container.
            </p>

            {activeLangs.map((lang) => {
              const spec = getSpec(lang);
              const candidates = extCandidates[lang] || [];
              return (
                <div
                  key={lang}
                  className="space-y-3 rounded-xl border border-slate-800 bg-slate-950/70 p-4"
                >
                  <div className="flex items-center justify-between">
                    <span className="rounded-md bg-indigo-500/20 px-2 py-0.5 text-xs font-semibold uppercase text-indigo-300">
                      Language: {lang}
                    </span>
                    <label className="flex items-center gap-1.5 text-xs text-slate-400">
                      <input
                        type="checkbox"
                        checked={spec.sync_tts_sub}
                        onChange={(e) =>
                          updateSpec(lang, (s) => ({ ...s, sync_tts_sub: e.target.checked }))
                        }
                        className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-indigo-500"
                      />
                      Auto-sync TTS subtitle
                    </label>
                  </div>

                  {/* TTS Subtitle Picker */}
                  <div>
                    <label className="flex items-center gap-1.5 text-xs font-medium text-slate-300">
                      <Volume2 className="h-3.5 w-3.5 text-emerald-400" />
                      TTS Voiceover Subtitle
                    </label>
                    <select
                      value={encodeSourceValue(spec.tts_sub)}
                      onChange={(e) =>
                        updateSpec(lang, (s) => ({
                          ...s,
                          tts_sub: decodeSourceValue(e.target.value, lang),
                        }))
                      }
                      className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
                    >
                      <option value="none">— None (Skip) —</option>
                      {embeddedSubs
                        .filter((s) => !s.is_bitmap)
                        .map((s) => (
                          <option key={`emb-${s.sub_index}`} value={`embedded:${s.sub_index}`}>
                            [Embedded #{s.sub_index}] {s.language} ({s.codec_name})
                            {s.title ? ` — ${s.title}` : ''}
                          </option>
                        ))}
                      {candidates.map((c) => (
                        <option key={`ext-${c.path}`} value={`external:${c.path}`}>
                          [External {c.format.toUpperCase()}] {c.file_name}
                        </option>
                      ))}
                    </select>
                  </div>

                  {/* Embed Subtitle Picker (ASS / SRT) */}
                  <div>
                    <label className="flex items-center gap-1.5 text-xs font-medium text-slate-300">
                      <Subtitles className="h-3.5 w-3.5 text-sky-400" />
                      Embed into Video (e.g. ASS version for descriptions / signs)
                    </label>
                    <select
                      value={encodeSourceValue(spec.embed_sub)}
                      onChange={(e) =>
                        updateSpec(lang, (s) => ({
                          ...s,
                          embed_sub:
                            e.target.value === 'none'
                              ? null
                              : decodeSourceValue(e.target.value, lang),
                        }))
                      }
                      className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
                    >
                      <option value="none">— Do not embed extra subtitle —</option>
                      {candidates.map((c) => (
                        <option key={`emb-ext-${c.path}`} value={`external:${c.path}`}>
                          [{c.is_ass ? 'ASS Styled' : c.format.toUpperCase()}] {c.file_name}
                        </option>
                      ))}
                    </select>
                    <input
                      type="text"
                      value={spec.embed_sub?.type === 'external' ? spec.embed_sub.path || '' : ''}
                      onChange={(e) => {
                        const val = e.target.value;
                        updateSpec(lang, (s) => ({
                          ...s,
                          embed_sub: val
                            ? decodeSourceValue(`external:${val}`, lang)
                            : null,
                        }));
                      }}
                      placeholder="Or paste custom external .ass / .srt path..."
                      className="mt-1.5 w-full rounded-lg border border-slate-800 bg-slate-950 px-3 py-1.5 text-xs text-slate-300 placeholder:text-slate-600 focus:border-indigo-500 focus:outline-none"
                    />
                  </div>
                </div>
              );
            })}
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
            {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
            Save Configuration
          </button>
        </div>
      </div>
    </div>
  );
}
