// denoise-check runs the call window's AI noise suppression (DeepFilterNet3,
// the same worklet and wasm the app bundles) offline, on a voice plus
// keyboard-click mix, and checks that the clicks go and the voice stays.
//
//   bun run test:denoise              synthetic voice (vowel-like harmonics)
//   bun run test:denoise speech.wav   your own 16-bit mono 48 kHz recording
//
// The worklet is loaded in a vm context with a stub AudioWorkletProcessor and
// fed 128-sample render quanta, like Chromium's audio thread does.
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const SR = 48_000;
const here = path.dirname(fileURLToPath(import.meta.url));
const pkg = path.join(here, "../node_modules/@lofcz/deepfilternet-web/dist");
// Prefer the stripped wasm from a build, so the check covers what ships.
const built = fs.existsSync(path.join(here, "../dist/assets"))
  ? fs.readdirSync(path.join(here, "../dist/assets")).find((f) => /^df_bg-.*\.wasm$/.test(f))
  : undefined;
const wasmPath = built ? path.join(here, "../dist/assets", built) : path.join(pkg, "df_bg.wasm");

function rng(seed) {
  let s = seed >>> 0;
  return () => (s = (s * 1664525 + 1013904223) >>> 0) / 4294967296;
}

function readWav(file) {
  const b = fs.readFileSync(file);
  for (let o = 12; o + 8 <= b.length; ) {
    const id = b.toString("ascii", o, o + 4);
    const n = b.readUInt32LE(o + 4);
    if (id === "data") {
      const x = new Float32Array(n / 2);
      for (let i = 0; i < x.length; i++) x[i] = b.readInt16LE(o + 8 + i * 2) / 32768;
      return x;
    }
    o += 8 + n + (n & 1);
  }
  throw new Error(`${file}: no data chunk`);
}

// A voice stand-in: a gliding 110-220 Hz pitch with 40 harmonics (to 8+ kHz)
// under three formant peaks, in 0.3-0.6 s syllables with gaps between.
function syntheticVoice(n) {
  const r = rng(1);
  const x = new Float32Array(n);
  const formants = [
    [700, 130],
    [1220, 70],
    [2600, 160],
  ];
  let phase = 0;
  for (let t = Math.floor(0.6 * SR); t < n; ) {
    const len = Math.floor((0.3 + 0.3 * r()) * SR);
    const f0a = 110 + 110 * r();
    const f0b = 110 + 110 * r();
    for (let i = 0; i < len && t + i < n; i++) {
      const f0 = f0a + ((f0b - f0a) * i) / len;
      phase += (2 * Math.PI * f0) / SR;
      let v = 0;
      for (let h = 1; h <= 40; h++) {
        const f = h * f0;
        if (f > 20_000) break;
        let g = 0.02;
        for (const [fc, bw] of formants) g += 1 / (1 + ((f - fc) / bw) ** 2);
        v += (g / h ** 0.5) * Math.sin(h * phase);
      }
      const env = Math.sin((Math.PI * i) / len) ** 0.5;
      x[t + i] = v * env;
    }
    t += len + Math.floor((0.25 + 0.35 * r()) * SR);
  }
  return x;
}

// Key presses: 4-14 ms broadband bursts with a fast decay, every 80-230 ms.
function keyboard(n) {
  const r = rng(7);
  const y = new Float32Array(n);
  for (let t = Math.floor(0.05 * SR); t < n; t += Math.floor((0.08 + 0.15 * r()) * SR)) {
    const len = Math.floor((0.004 + 0.01 * r()) * SR);
    const a = 0.6 + 0.4 * r();
    let lp = 0;
    for (let i = 0; i < len && t + i < n; i++) {
      const w = r() * 2 - 1;
      lp = 0.6 * lp + 0.4 * w;
      y[t + i] += a * (w - 0.5 * lp) * Math.exp(-i / (len / 4));
    }
  }
  return y;
}

function rms(x, a = 0, b = x.length) {
  let s = 0;
  for (let i = a; i < b; i++) s += x[i] * x[i];
  return Math.sqrt(s / Math.max(1, b - a));
}
const dB = (v) => 20 * Math.log10(v);

function loadProcessor() {
  let Proc;
  const ctx = {
    AudioWorkletProcessor: class {
      constructor() {
        this.port = { onmessage: null, postMessage() {} };
      }
    },
    registerProcessor: (_name, c) => (Proc = c),
    sampleRate: SR,
    currentTime: 0,
    console,
  };
  ctx.globalThis = ctx;
  vm.createContext(ctx);
  // Like Chromium's worklet scope: no TextDecoder or crypto until the shim
  // the call window loads first.
  vm.runInContext(fs.readFileSync(path.join(here, "../src/stream/workletShim.js"), "utf8"), ctx);
  // Fail loudly instead of the worklet's silent pass-through on a bad init.
  const src = fs
    .readFileSync(path.join(pkg, "worklet.js"), "utf8")
    .replace("} catch {\n        this.#ready = false;", "} catch (e) { throw e;");
  vm.runInContext(src, ctx);
  const wasm = fs.readFileSync(wasmPath);
  // The module and options must come from the worklet's own realm.
  const opts = vm.runInContext(
    "(b) => ({ processorOptions: { wasmModule: new WebAssembly.Module(new Uint8Array(b)), attenuationLimit: 100 } })",
    ctx,
  )(wasm);
  return () => new Proc(opts);
}

function run(make, x) {
  const p = make();
  const y = new Float32Array(x.length);
  let worst = 0;
  const t0 = performance.now();
  for (let i = 0; i + 128 <= x.length; i += 128) {
    const out = new Float32Array(128);
    const t = performance.now();
    p.process([[x.subarray(i, i + 128)]], [[out]], {});
    worst = Math.max(worst, performance.now() - t);
    y.set(out, i);
  }
  return { y, ms: performance.now() - t0, worst };
}

// The model delays its output; find by how much.
function lag(a, b) {
  const c = (l) => {
    let s = 0;
    for (let i = 0; i + l < a.length; i += 2) s += a[i] * b[i + l];
    return s;
  };
  let best = -Infinity;
  let at = 0;
  for (let l = 0; l < 4800; l++) {
    const s = c(l);
    if (s > best) [best, at] = [s, l];
  }
  return at;
}

const wav = process.argv[2];
const voice = wav ? readWav(wav) : syntheticVoice(8 * SR);
const floor = rng(11);
for (let i = 0; i < voice.length; i++) voice[i] += (floor() * 2 - 1) * 0.0017; // -60 dBFS mic hiss
const keys = keyboard(voice.length);
const k = (0.5 * rms(voice)) / rms(keys); // clicks 6 dB under the voice: a loud mechanical keyboard
for (let i = 0; i < keys.length; i++) keys[i] *= k;
const mix = voice.map((v, i) => v + keys[i]);

const make = loadProcessor();
const clean = run(make, voice);
const noisy = run(make, mix);
const d = lag(voice, clean.y);
const skip = SR; // the model's first second is warm-up
const end = voice.length - d - 1;

// Voice frames vs pauses, by the clean voice's 10 ms level.
let pauseIn = 0;
let pauseOut = 0;
let voiceIn = 0;
let voiceOut = 0;
for (let f = skip; f + 480 < end; f += 480) {
  const speaking = rms(voice, f, f + 480) > 0.01;
  for (let i = f; i < f + 480; i++) {
    const n0 = (mix[i] - voice[i]) ** 2;
    const n1 = (noisy.y[i + d] - voice[i]) ** 2;
    if (speaking) {
      voiceIn += voice[i] ** 2;
      voiceOut += clean.y[i + d] ** 2;
    } else {
      pauseIn += n0;
      pauseOut += n1;
    }
  }
}
const seconds = voice.length / SR;
const clickCut = 10 * Math.log10(pauseOut / pauseIn);
const voiceKept = 10 * Math.log10(voiceOut / voiceIn);
console.log(`wasm: ${path.relative(process.cwd(), wasmPath)} (${(fs.statSync(wasmPath).size / 1e6).toFixed(1)} MB)`);
console.log(`input: ${wav ?? "synthetic voice"}, ${seconds.toFixed(1)} s; clicks ${dB(rms(keys)).toFixed(1)} dBFS, voice ${dB(rms(voice)).toFixed(1)} dBFS`);
console.log(`latency ${((d / SR) * 1000).toFixed(1)} ms; CPU ${((noisy.ms / (seconds * 1000)) * 100).toFixed(1)}% of one core; slowest 128-sample quantum ${noisy.worst.toFixed(2)} ms`);
console.log(`keyboard in pauses: ${clickCut.toFixed(1)} dB; voice level kept: ${voiceKept.toFixed(1)} dB`);

const fails = [];
if (!(clickCut < -20)) fails.push(`clicks only cut ${clickCut.toFixed(1)} dB (want < -20 dB)`);
if (!(Math.abs(voiceKept) < 3)) fails.push(`voice level changed ${voiceKept.toFixed(1)} dB (want within 3 dB)`);
if (fails.length) {
  console.error(`FAIL: ${fails.join("; ")}`);
  process.exit(1);
}
console.log("ok");
