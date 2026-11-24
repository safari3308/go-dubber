import { KokoroTTS } from 'kokoro-js';

let ttsInstance: KokoroTTS | null = null;
let activeDevice: 'webgpu' | 'wasm' = 'wasm';

interface InitMessage {
  type: 'init';
}

interface SynthesizeMessage {
  type: 'synthesize';
  reqId: string;
  text: string;
  voice: string;
  speed: number;
}

type WorkerInMessage = InitMessage | SynthesizeMessage;

function encodePCM16WAV(samples: Float32Array, sampleRate: number): ArrayBuffer {
  const numChannels = 1;
  const bitsPerSample = 16;
  const bytesPerSample = bitsPerSample / 8;
  const dataLength = samples.length * bytesPerSample;
  const buffer = new ArrayBuffer(44 + dataLength);
  const view = new DataView(buffer);

  const writeStr = (offset: number, str: string) => {
    for (let i = 0; i < str.length; i++) {
      view.setUint8(offset + i, str.charCodeAt(i));
    }
  };

  writeStr(0, 'RIFF');
  view.setUint32(4, 36 + dataLength, true);
  writeStr(8, 'WAVE');
  writeStr(12, 'fmt ');
  view.setUint32(16, 16, true); // PCM chunk size
  view.setUint16(20, 1, true); // PCM format
  view.setUint16(22, numChannels, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * numChannels * bytesPerSample, true);
  view.setUint16(32, numChannels * bytesPerSample, true);
  view.setUint16(34, bitsPerSample, true);
  writeStr(36, 'data');
  view.setUint32(40, dataLength, true);

  let offset = 44;
  for (let i = 0; i < samples.length; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    offset += 2;
  }

  return buffer;
}

async function hasWebGPU(): Promise<boolean> {
  try {
    const nav = self.navigator as Navigator & { gpu?: { requestAdapter: () => Promise<unknown> } };
    if (!nav?.gpu) return false;
    const adapter = await nav.gpu.requestAdapter();
    return Boolean(adapter);
  } catch {
    return false;
  }
}

async function initModel() {
  if (ttsInstance) {
    self.postMessage({ type: 'ready', device: activeDevice });
    return;
  }

  const modelId = 'onnx-community/Kokoro-82M-v1.0-ONNX';
  const useGPU = await hasWebGPU();
  const preferredDevice: 'webgpu' | 'wasm' = useGPU ? 'webgpu' : 'wasm';

  const progressCallback = (info: { progress?: number; status?: string }) => {
    if (typeof info?.progress === 'number') {
      self.postMessage({
        type: 'progress',
        progress: Math.round(info.progress),
        status: info.status || 'loading',
      });
    }
  };

  try {
    activeDevice = preferredDevice;
    ttsInstance = await KokoroTTS.from_pretrained(modelId, {
      dtype: preferredDevice === 'webgpu' ? 'fp32' : 'q8',
      device: preferredDevice,
      progress_callback: progressCallback,
    });
    self.postMessage({ type: 'ready', device: activeDevice });
  } catch (err) {
    if (preferredDevice === 'webgpu') {
      // Fallback cleanly to WASM q8 if WebGPU fails
      try {
        activeDevice = 'wasm';
        ttsInstance = await KokoroTTS.from_pretrained(modelId, {
          dtype: 'q8',
          device: 'wasm',
          progress_callback: progressCallback,
        });
        self.postMessage({ type: 'ready', device: activeDevice });
        return;
      } catch (wasmErr) {
        self.postMessage({
          type: 'error',
          error: wasmErr instanceof Error ? wasmErr.message : String(wasmErr),
        });
        return;
      }
    }
    self.postMessage({
      type: 'error',
      error: err instanceof Error ? err.message : String(err),
    });
  }
}

self.onmessage = async (event: MessageEvent<WorkerInMessage>) => {
  const msg = event.data;
  if (msg.type === 'init') {
    await initModel();
    return;
  }

  if (msg.type === 'synthesize') {
    if (!ttsInstance) {
      self.postMessage({
        type: 'result',
        reqId: msg.reqId,
        error: 'KokoroJS model is not loaded yet',
      });
      return;
    }

    try {
      const audio = await ttsInstance.generate(msg.text, {
        voice: (msg.voice || 'af_heart') as Parameters<KokoroTTS['generate']>[1] extends {
          voice?: infer V;
        }
          ? V
          : never,
        speed: msg.speed || 1.1,
      });
      const wavBuffer = encodePCM16WAV(audio.audio, audio.sampling_rate || 24000);
      self.postMessage({ type: 'result', reqId: msg.reqId, wavBuffer }, { transfer: [wavBuffer] });
    } catch (err) {
      self.postMessage({
        type: 'result',
        reqId: msg.reqId,
        error: err instanceof Error ? err.message : String(err),
      });
    }
  }
};
