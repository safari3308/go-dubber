import { useCallback, useEffect, useRef, useState } from 'react';

export type BrowserKokoroState = 'off' | 'loading' | 'ready' | 'error';

export interface BrowserTTSRequest {
  req_id: string;
  text: string;
  voice: string;
  speed: number;
}

export function useBrowserKokoro() {
  const [state, setState] = useState<BrowserKokoroState>('off');
  const [progress, setProgress] = useState(0);
  const [device, setDevice] = useState<'webgpu' | 'wasm' | ''>('');
  const [voice, setVoice] = useState('af_heart');
  const [error, setError] = useState('');
  const workerRef = useRef<Worker | null>(null);

  const reportStatusToServer = useCallback(
    async (ready: boolean, dev: string, selectedVoice: string) => {
      try {
        await fetch('/api/browser-tts/status', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ ready, device: dev, voice: selectedVoice }),
        });
      } catch {
        // ignore network errors during unload
      }
    },
    []
  );

  const disable = useCallback(() => {
    if (workerRef.current) {
      workerRef.current.terminate();
      workerRef.current = null;
    }
    setState('off');
    setProgress(0);
    setDevice('');
    setError('');
    void reportStatusToServer(false, '', voice);
  }, [reportStatusToServer, voice]);

  const enable = useCallback(() => {
    if (workerRef.current) return;
    setState('loading');
    setProgress(0);
    setError('');

    const worker = new Worker(new URL('../workers/kokoro.worker.ts', import.meta.url), {
      type: 'module',
    });
    workerRef.current = worker;

    worker.onmessage = async (event: MessageEvent) => {
      const msg = event.data;
      if (msg.type === 'progress') {
        setProgress(msg.progress || 0);
      } else if (msg.type === 'ready') {
        const dev = msg.device || 'wasm';
        setDevice(dev);
        setProgress(100);
        setState('ready');
        void reportStatusToServer(true, dev, voice);
      } else if (msg.type === 'error') {
        setError(msg.error || 'Failed to load KokoroJS');
        setState('error');
        void reportStatusToServer(false, '', voice);
      } else if (msg.type === 'result') {
        const { reqId, wavBuffer, error: synthErr } = msg;
        try {
          if (synthErr) {
            await fetch(`/api/browser-tts/result/${encodeURIComponent(reqId)}`, {
              method: 'POST',
              headers: { 'X-TTS-Error': String(synthErr) },
            });
          } else if (wavBuffer) {
            await fetch(`/api/browser-tts/result/${encodeURIComponent(reqId)}`, {
              method: 'POST',
              headers: { 'Content-Type': 'audio/wav' },
              body: new Blob([wavBuffer], { type: 'audio/wav' }),
            });
          }
        } catch {
          // ignore
        }
      }
    };

    worker.onerror = (err) => {
      setError(err.message || 'KokoroJS worker crashed');
      setState('error');
      void reportStatusToServer(false, '', voice);
    };

    worker.postMessage({ type: 'init' });
  }, [reportStatusToServer, voice]);

  const toggle = useCallback(() => {
    if (state === 'off' || state === 'error') {
      enable();
    } else {
      disable();
    }
  }, [state, enable, disable]);

  const handleTTSRequest = useCallback((req: BrowserTTSRequest) => {
    if (!workerRef.current) {
      void fetch(`/api/browser-tts/result/${encodeURIComponent(req.req_id)}`, {
        method: 'POST',
        headers: { 'X-TTS-Error': 'Browser KokoroJS worker is not running' },
      });
      return;
    }
    workerRef.current.postMessage({
      type: 'synthesize',
      reqId: req.req_id,
      text: req.text,
      voice: req.voice || 'af_heart',
      speed: req.speed || 1.1,
    });
  }, []);

  // Notify backend if tab closes while Browser KokoroJS was active
  useEffect(() => {
    const onBeforeUnload = () => {
      if (workerRef.current) {
        navigator.sendBeacon(
          '/api/browser-tts/status',
          new Blob([JSON.stringify({ ready: false })], { type: 'application/json' })
        );
      }
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => {
      window.removeEventListener('beforeunload', onBeforeUnload);
      if (workerRef.current) {
        workerRef.current.terminate();
        workerRef.current = null;
      }
    };
  }, []);

  return {
    state,
    progress,
    device,
    voice,
    setVoice,
    error,
    toggle,
    handleTTSRequest,
  };
}
