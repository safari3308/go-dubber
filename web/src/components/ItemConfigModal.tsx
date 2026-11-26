import { useEffect, useState } from 'react';
import {
  AudioLines,
  Check,
  Copy,
  Download,
  FileText,
  Loader2,
  Mic,
  Save,
  Sparkles,
  Subtitles,
  Volume2,
  X,
} from 'lucide-react';
import { api } from '../api/client';
import type {
  AudioStreamDetails,
  CandidateSubtitle,
  EmbeddedSubInfo,
  ItemConfig,
  JobItem,
  LanguageDubSpec,
  SubtitleSource,
} from '../types/api';

interface ItemConfigModalProps {
  job: JobItem | null;
  geminiReady?: boolean;
  onClose: () => void;
  onSave: (jobId: string, cfg: ItemConfig) => Promise<void>;
  onJobUpdated?: () => Promise<void>;
}

const SUPPORTED_LANGS: { code: 'en' | 'vi'; label: string }[] = [
  { code: 'en', label: 'English (EN)' },
  { code: 'vi', label: 'Vietnamese (VI)' },
];

export function ItemConfigModal({
  job,
  geminiReady,
  onClose,
  onSave,
  onJobUpdated,
}: ItemConfigModalProps) {
  const [cfg, setCfg] = useState<ItemConfig | null>(null);
  const [audioStreams, setAudioStreams] = useState<AudioStreamDetails[]>([]);
  const [embeddedSubs, setEmbeddedSubs] = useState<EmbeddedSubInfo[]>([]);
  const [extCandidates, setExtCandidates] = useState<Record<string, CandidateSubtitle[]>>({});
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [extractingKey, setExtractingKey] = useState<string | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!job) return;
    setCfg(structuredClone(job.config));
    setAudioStreams(job.video_info?.audio_streams || []);
    setError('');
    setLoading(true);
    api
      .inspectVideo(job.source_path, ['en', 'vi'])
      .then((res) => {
        setAudioStreams(res.video_info.audio_streams || []);
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
        setAudioStreams(job.video_info?.audio_streams || []);
        setEmbeddedSubs(job.video_info?.embedded_sub_streams || []);
      })
      .finally(() => setLoading(false));
  }, [job]);

  if (!job || !cfg) return null;

  const toggleLang = (code: 'en' | 'vi', checked: boolean) => {
    setCfg((prev) => {
      if (!prev) return prev;
      const set = new Set(prev.dub_languages || []);
      if (checked) set.add(code);
      else set.delete(code);
      return { ...prev, dub_languages: Array.from(set) };
    });
  };

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

  const handleExtract = async (
    subIndex: number,
    format: 'original' | 'srt',
    mode: 'copy' | 'download'
  ) => {
    const key = `${subIndex}-${format}-${mode}`;
    setExtractingKey(key);
    setError('');
    try {
      const res = await api.extractSubtitleJSON(job.source_path, subIndex, format);
      if (mode === 'copy') {
        await navigator.clipboard.writeText(res.content);
        setCopiedKey(key);
        setTimeout(() => setCopiedKey((curr) => (curr === key ? null : curr)), 2500);
      } else {
        const blob = new Blob([res.content], { type: 'text/plain;charset=utf-8' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = res.filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
        URL.revokeObjectURL(url);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to extract subtitle');
    } finally {
      setExtractingKey(null);
    }
  };

  const handleGeminiProcessTrack = async (subIndex: number, targetLang: 'en' | 'vi') => {
    const key = `gemini-${subIndex}-${targetLang}`;
    setExtractingKey(key);
    setError('');
    try {
      const res = await api.processGeminiSubtitle(job.id, {
        source_type: 'embedded',
        embedded_index: subIndex,
        target_lang: targetLang,
        translate: true,
        clean_for_tts: true,
        style: cfg.gemini_style || 'standard',
      });
      setCfg(structuredClone(res.job.config));
      if (onJobUpdated) await onJobUpdated();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Google AI processing failed');
    } finally {
      setExtractingKey(null);
    }
  };

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      await onSave(job.id, cfg);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save config');
    } finally {
      setSaving(false);
    }
  };

  const textEmbeddedSubs = embeddedSubs.filter((s) => !s.is_bitmap);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
      <div className="flex max-h-[90vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-slate-800 bg-slate-900 shadow-2xl">
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

          {/* Supported TTS Language Checkboxes (EN / VI only) */}
          <div>
            <label className="block text-xs font-medium text-slate-300">
              Languages to TTS Dub (Supported: English & Vietnamese)
            </label>
            <div className="mt-2 flex flex-wrap gap-3">
              {SUPPORTED_LANGS.map(({ code, label }) => {
                const checked = (cfg.dub_languages || []).includes(code);
                return (
                  <label
                    key={code}
                    className={`flex cursor-pointer items-center gap-2 rounded-lg border px-3.5 py-2 text-xs font-medium transition ${
                      checked
                        ? 'border-indigo-500/60 bg-indigo-500/15 text-indigo-200'
                        : 'border-slate-700 bg-slate-950 text-slate-300 hover:border-slate-600'
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) => toggleLang(code, e.target.checked)}
                      className="h-4 w-4 rounded border-slate-600 bg-slate-900 text-indigo-500"
                    />
                    <span>{label}</span>
                  </label>
                );
              })}
            </div>
          </div>

          {/* General Per-Item Settings */}
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <div>
              <label className="block text-xs font-medium text-slate-300">
                Original Audio Lang / Fallback
              </label>
              <div className="mt-1 flex gap-2">
                <input
                  type="text"
                  value={cfg.original_language}
                  onChange={(e) => setCfg({ ...cfg, original_language: e.target.value })}
                  placeholder="ja, zh, en"
                  className="w-2/3 rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
                />
                <input
                  type="number"
                  min={0}
                  value={cfg.original_audio_index}
                  onChange={(e) =>
                    setCfg({ ...cfg, original_audio_index: parseInt(e.target.value, 10) || 0 })
                  }
                  className="w-1/3 rounded-lg border border-slate-700 bg-slate-950 px-2.5 py-2 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
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
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300">Volume Boost</label>
              <input
                type="text"
                value={cfg.volume_boost}
                onChange={(e) => setCfg({ ...cfg, volume_boost: e.target.value })}
                placeholder="2.0"
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>
          </div>

          {/* Toggles */}
          <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
            {[
              { key: 'use_gpu', label: 'Use GPU Encoding' },
              { key: 'skip_encode', label: 'Skip Video Re-encode' },
              { key: 'skip_sub_sync', label: 'Skip Subtitle Sync' },
              { key: 'force_reprocess', label: 'Force Reprocess' },
              { key: 'ai_track_as_first_track', label: 'AI Audio as Track #0' },
              { key: 'drop_song_subtitles', label: 'Drop Song/Karaoke Subs' },
              { key: 'use_gemini', label: 'Use Google AI (Gemini)' },
              { key: 'gemini_clean_for_tts', label: 'AI: Clean Descriptions' },
              { key: 'gemini_translate', label: 'AI: Translate Dialogue' },
            ].map(({ key, label }) => (
              <label
                key={key}
                className="flex cursor-pointer items-center gap-2 rounded-lg border border-slate-800 bg-slate-950/60 px-3 py-2 text-xs text-slate-300"
              >
                <input
                  type="checkbox"
                  checked={Boolean(cfg[key as keyof ItemConfig])}
                  onChange={(e) => setCfg({ ...cfg, [key]: e.target.checked })}
                  className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-900 text-indigo-500"
                />
                <span>{label}</span>
              </label>
            ))}
          </div>

          {/* Per-Video Google AI Translation Style */}
          {(cfg.use_gemini || geminiReady) && (
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-violet-500/20 bg-violet-500/5 px-4 py-2.5">
              <div className="flex items-center gap-2 text-xs font-medium text-violet-200">
                <Sparkles className="h-3.5 w-3.5 text-violet-400" />
                <span>Google AI Subtitle Style</span>
              </div>
              <select
                value={cfg.gemini_style || 'standard'}
                onChange={(e) =>
                  setCfg({ ...cfg, gemini_style: e.target.value as 'standard' | 'donghua' })
                }
                className="rounded-lg border border-slate-700 bg-slate-950 px-3 py-1.5 text-xs text-slate-100 focus:border-violet-500 focus:outline-none"
              >
                <option value="standard">Standard (Modern / Normal Videos)</option>
                <option value="donghua">Donghua / Xianxia (Hán Việt for TTS)</option>
              </select>
            </div>
          )}

          {/* Available Audio Tracks & Source (Origin) Audio Selector */}
          <div className="space-y-2.5 rounded-xl border border-slate-800 bg-slate-950/60 p-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <AudioLines className="h-4 w-4 text-sky-400" />
                <h4 className="text-sm font-semibold text-white">
                  Available Audio Tracks & Source (Origin) Audio
                </h4>
              </div>
              <span className="text-xs text-slate-500">
                {audioStreams.length === 0
                  ? 'Silent / No audio'
                  : `${audioStreams.length} track(s)`}
              </span>
            </div>

            {audioStreams.length === 0 ? (
              <div className="flex items-start gap-2.5 rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-xs text-emerald-200">
                <Mic className="mt-0.5 h-4 w-4 shrink-0 text-emerald-400" />
                <div>
                  <div className="font-semibold">No audio tracks detected (Recording Mode)</div>
                  <p className="mt-0.5 text-emerald-200/80">
                    The generated TTS voiceover will be added directly as the primary audio track
                    (Track #0) without mixing background audio.
                  </p>
                </div>
              </div>
            ) : (
              <div className="space-y-2">
                <label className="block text-xs text-slate-400">
                  Select which audio track to use as background/source audio for dubbing:
                </label>
                <div className="space-y-1.5">
                  <label className="flex cursor-pointer items-center gap-2.5 rounded-lg border border-slate-800 bg-slate-900/70 px-3 py-2 text-xs text-slate-200 hover:border-slate-700">
                    <input
                      type="radio"
                      name="source_audio"
                      checked={cfg.source_audio_index === undefined || cfg.source_audio_index === null}
                      onChange={() => setCfg({ ...cfg, source_audio_index: null })}
                      className="text-indigo-500"
                    />
                    <span className="font-medium">Auto-select</span>
                    <span className="text-slate-400">
                      (match original language &ldquo;{cfg.original_language || 'default'}&rdquo; /
                      first non-AI track)
                    </span>
                  </label>

                  {audioStreams.map((as) => (
                    <label
                      key={as.audio_index}
                      className="flex cursor-pointer flex-wrap items-center justify-between gap-2 rounded-lg border border-slate-800 bg-slate-900/70 px-3 py-2 text-xs text-slate-200 hover:border-slate-700"
                    >
                      <div className="flex items-center gap-2.5">
                        <input
                          type="radio"
                          name="source_audio"
                          checked={cfg.source_audio_index === as.audio_index}
                          onChange={() => setCfg({ ...cfg, source_audio_index: as.audio_index })}
                          className="text-indigo-500"
                        />
                        <span className="font-semibold">Track #{as.audio_index}</span>
                        <span className="rounded bg-slate-800 px-1.5 py-0.5 uppercase text-sky-300">
                          {as.language || 'und'}
                        </span>
                        {as.codec_name && (
                          <span className="rounded bg-slate-800 px-1.5 py-0.5 uppercase text-slate-400">
                            {as.codec_name}
                            {as.channels ? ` ${as.channels}ch` : ''}
                          </span>
                        )}
                        {as.title && <span className="text-slate-400">— {as.title}</span>}
                      </div>
                      {as.is_kokoro && (
                        <span className="rounded bg-fuchsia-500/20 px-1.5 py-0.5 text-[10px] font-medium text-fuchsia-300">
                          AI Dub
                        </span>
                      )}
                    </label>
                  ))}

                  <label className="flex cursor-pointer items-center gap-2.5 rounded-lg border border-slate-800 bg-slate-900/70 px-3 py-2 text-xs text-slate-300 hover:border-slate-700">
                    <input
                      type="radio"
                      name="source_audio"
                      checked={cfg.source_audio_index === -1}
                      onChange={() => setCfg({ ...cfg, source_audio_index: -1 })}
                      className="text-indigo-500"
                    />
                    <span className="font-medium">None (TTS voice only)</span>
                    <span className="text-slate-500">— mute original audio completely</span>
                  </label>
                </div>
              </div>
            )}
          </div>

          {/* Embedded Subtitles — 1-Click Extract for Gemini Chat */}
          {textEmbeddedSubs.length > 0 && (
            <div className="space-y-2.5 rounded-xl border border-slate-800 bg-slate-950/60 p-4">
              <div className="flex items-center gap-2">
                <FileText className="h-4 w-4 text-amber-400" />
                <h4 className="text-sm font-semibold text-white">
                  Extract Embedded Subtitles (for Gemini Translation / Filtering)
                </h4>
              </div>
              <div className="space-y-2">
                {textEmbeddedSubs.map((sub) => {
                  const isASS = sub.codec_name === 'ass' || sub.codec_name === 'ssa';
                  const copyKey = `${sub.sub_index}-original-copy`;
                  const dlOrigKey = `${sub.sub_index}-original-download`;
                  const dlSrtKey = `${sub.sub_index}-srt-download`;
                  return (
                    <div
                      key={sub.sub_index}
                      className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-slate-800 bg-slate-900/80 px-3 py-2 text-xs"
                    >
                      <div className="min-w-0 flex-1">
                        <span className="font-semibold text-slate-200">Track #{sub.sub_index}</span>
                        <span className="ml-2 rounded bg-slate-800 px-1.5 py-0.5 uppercase text-indigo-300">
                          {sub.language}
                        </span>
                        <span className="ml-1.5 rounded bg-slate-800 px-1.5 py-0.5 uppercase text-slate-400">
                          {sub.codec_name}
                        </span>
                        {sub.title && (
                          <span className="ml-2 text-slate-400">— {sub.title}</span>
                        )}
                      </div>

                      <div className="flex items-center gap-1.5">
                        <button
                          type="button"
                          disabled={extractingKey !== null}
                          onClick={() => void handleExtract(sub.sub_index, 'original', 'copy')}
                          className="inline-flex items-center gap-1 rounded-md bg-slate-800 px-2.5 py-1 text-[11px] font-medium text-slate-200 hover:bg-slate-700 disabled:opacity-40"
                        >
                          {extractingKey === copyKey ? (
                            <Loader2 className="h-3 w-3 animate-spin" />
                          ) : copiedKey === copyKey ? (
                            <Check className="h-3 w-3 text-emerald-400" />
                          ) : (
                            <Copy className="h-3 w-3 text-amber-400" />
                          )}
                          {copiedKey === copyKey ? 'Copied!' : 'Copy'}
                        </button>

                        <button
                          type="button"
                          disabled={extractingKey !== null}
                          onClick={() => void handleExtract(sub.sub_index, 'original', 'download')}
                          className="inline-flex items-center gap-1 rounded-md bg-slate-800 px-2.5 py-1 text-[11px] font-medium text-slate-200 hover:bg-slate-700 disabled:opacity-40"
                        >
                          {extractingKey === dlOrigKey ? (
                            <Loader2 className="h-3 w-3 animate-spin" />
                          ) : (
                            <Download className="h-3 w-3 text-sky-400" />
                          )}
                          {isASS ? 'Download .ASS' : 'Download'}
                        </button>

                        {isASS && (
                          <button
                            type="button"
                            disabled={extractingKey !== null}
                            onClick={() => void handleExtract(sub.sub_index, 'srt', 'download')}
                            className="inline-flex items-center gap-1 rounded-md bg-slate-800 px-2.5 py-1 text-[11px] font-medium text-slate-300 hover:bg-slate-700 disabled:opacity-40"
                          >
                            {extractingKey === dlSrtKey ? (
                              <Loader2 className="h-3 w-3 animate-spin" />
                            ) : (
                              <Download className="h-3 w-3 text-emerald-400" />
                            )}
                            .SRT
                          </button>
                        )}

                        {geminiReady && (
                          <>
                            <button
                              type="button"
                              disabled={extractingKey !== null}
                              onClick={() => void handleGeminiProcessTrack(sub.sub_index, 'vi')}
                              title="Translate & clean for TTS into Vietnamese using Google AI, and attach as TTS subtitle"
                              className="inline-flex items-center gap-1 rounded-md bg-violet-600/25 px-2.5 py-1 text-[11px] font-medium text-violet-200 ring-1 ring-violet-500/40 hover:bg-violet-600/40 disabled:opacity-40"
                            >
                              {extractingKey === `gemini-${sub.sub_index}-vi` ? (
                                <Loader2 className="h-3 w-3 animate-spin" />
                              ) : (
                                <Sparkles className="h-3 w-3 text-violet-300" />
                              )}
                              AI → VI
                            </button>
                            <button
                              type="button"
                              disabled={extractingKey !== null}
                              onClick={() => void handleGeminiProcessTrack(sub.sub_index, 'en')}
                              title="Translate & clean for TTS into English using Google AI, and attach as TTS subtitle"
                              className="inline-flex items-center gap-1 rounded-md bg-violet-600/25 px-2.5 py-1 text-[11px] font-medium text-violet-200 ring-1 ring-violet-500/40 hover:bg-violet-600/40 disabled:opacity-40"
                            >
                              {extractingKey === `gemini-${sub.sub_index}-en` ? (
                                <Loader2 className="h-3 w-3 animate-spin" />
                              ) : (
                                <Sparkles className="h-3 w-3 text-violet-300" />
                              )}
                              AI → EN
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {/* Dual-Subtitle Configuration per Ticked Language */}
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

            {SUPPORTED_LANGS.filter(({ code }) => (cfg.dub_languages || []).includes(code)).map(
              ({ code: lang, label }) => {
                const spec = getSpec(lang);
                const candidates = extCandidates[lang] || [];
                return (
                  <div
                    key={lang}
                    className="space-y-3 rounded-xl border border-slate-800 bg-slate-950/70 p-4"
                  >
                    <div className="flex items-center justify-between">
                      <span className="rounded-md bg-indigo-500/20 px-2 py-0.5 text-xs font-semibold text-indigo-300">
                        {label}
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

                    {/* Output Audio Track Action: Add New vs Replace Existing Track */}
                    {audioStreams.length > 0 && (
                      <div>
                        <label className="flex items-center gap-1.5 text-xs font-medium text-slate-300">
                          <AudioLines className="h-3.5 w-3.5 text-fuchsia-400" />
                          Output Audio Track Action
                        </label>
                        <select
                          value={
                            spec.replace_audio_index !== undefined &&
                            spec.replace_audio_index !== null &&
                            spec.replace_audio_index >= 0
                              ? String(spec.replace_audio_index)
                              : '-1'
                          }
                          onChange={(e) => {
                            const val = parseInt(e.target.value, 10);
                            updateSpec(lang, (s) => ({
                              ...s,
                              replace_audio_index: val,
                            }));
                          }}
                          className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 text-xs text-slate-100 focus:border-indigo-500 focus:outline-none"
                        >
                          <option value="-1">Add as a new audio track (keep existing tracks)</option>
                          {audioStreams.map((as) => (
                            <option key={`rep-${as.audio_index}`} value={String(as.audio_index)}>
                              Replace Track #{as.audio_index} — {as.language || 'und'} (
                              {as.codec_name || 'audio'}
                              {as.channels ? `, ${as.channels}ch` : ''})
                              {as.title ? ` — ${as.title}` : ''}
                              {as.is_kokoro ? ' [AI Dub]' : ''}
                            </option>
                          ))}
                        </select>
                      </div>
                    )}

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
                        {textEmbeddedSubs.map((s) => (
                          <option key={`emb-${s.sub_index}`} value={`embedded:${s.sub_index}`}>
                            [Embedded #{s.sub_index}] {s.language} ({s.codec_name})
                            {s.title ? ` — ${s.title}` : ''}
                          </option>
                        ))}
                        {spec.tts_sub?.type === 'external' &&
                          spec.tts_sub.path &&
                          !candidates.some((c) => c.path === spec.tts_sub.path) && (
                            <option value={`external:${spec.tts_sub.path}`}>
                              [Uploaded/Custom] {spec.tts_sub.description || spec.tts_sub.path}
                            </option>
                          )}
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
                        {spec.embed_sub?.type === 'external' &&
                          spec.embed_sub.path &&
                          !candidates.some((c) => c.path === spec.embed_sub?.path) && (
                            <option value={`external:${spec.embed_sub.path}`}>
                              [Uploaded/Custom] {spec.embed_sub.description || spec.embed_sub.path}
                            </option>
                          )}
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
                            embed_sub: val ? decodeSourceValue(`external:${val}`, lang) : null,
                          }));
                        }}
                        placeholder="Or paste custom external .ass / .srt pathname..."
                        className="mt-1.5 w-full rounded-lg border border-slate-800 bg-slate-950 px-3 py-1.5 font-mono text-xs text-slate-300 placeholder:text-slate-600 focus:border-indigo-500 focus:outline-none"
                      />
                    </div>
                  </div>
                );
              }
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
            Save Configuration
          </button>
        </div>
      </div>
    </div>
  );
}
