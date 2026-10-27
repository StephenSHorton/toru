// workletShim.js is loaded into the call window's AudioWorkletGlobalScope
// before the DeepFilterNet worklet. Chromium's worklet scope has no
// TextDecoder and no crypto, and the model's wasm-bindgen glue creates a
// TextDecoder at load time: without these the processor never registers and
// "ai" mode silently falls back to Chromium's suppressor. Plain JS, served
// as-is (?url), so it runs before any module that needs it.

if (typeof globalThis.TextDecoder === "undefined") {
  // UTF-8 only, which is all the glue asks for (error and panic strings).
  globalThis.TextDecoder = class TextDecoder {
    constructor(label = "utf-8") {
      this.encoding = String(label).toLowerCase();
    }
    decode(input) {
      if (!input) return "";
      const b = input instanceof Uint8Array ? input : new Uint8Array(input.buffer ?? input);
      let out = "";
      for (let i = 0; i < b.length; ) {
        const c = b[i++];
        let cp;
        if (c < 0x80) cp = c;
        else if (c >= 0xf0) cp = ((c & 0x07) << 18) | ((b[i++] & 0x3f) << 12) | ((b[i++] & 0x3f) << 6) | (b[i++] & 0x3f);
        else if (c >= 0xe0) cp = ((c & 0x0f) << 12) | ((b[i++] & 0x3f) << 6) | (b[i++] & 0x3f);
        else cp = ((c & 0x1f) << 6) | (b[i++] & 0x3f);
        out += String.fromCodePoint(cp);
      }
      return out;
    }
  };
}

if (typeof globalThis.TextEncoder === "undefined") {
  globalThis.TextEncoder = class TextEncoder {
    get encoding() {
      return "utf-8";
    }
    encode(s = "") {
      const out = [];
      for (const ch of String(s)) {
        const cp = ch.codePointAt(0);
        if (cp < 0x80) out.push(cp);
        else if (cp < 0x800) out.push(0xc0 | (cp >> 6), 0x80 | (cp & 0x3f));
        else if (cp < 0x10000) out.push(0xe0 | (cp >> 12), 0x80 | ((cp >> 6) & 0x3f), 0x80 | (cp & 0x3f));
        else out.push(0xf0 | (cp >> 18), 0x80 | ((cp >> 12) & 0x3f), 0x80 | ((cp >> 6) & 0x3f), 0x80 | (cp & 0x3f));
      }
      return new Uint8Array(out);
    }
  };
}

if (typeof globalThis.crypto === "undefined") {
  // The model only seeds its RNG with this (dither / init), not for secrets.
  globalThis.crypto = {
    getRandomValues(a) {
      for (let i = 0; i < a.length; i++) a[i] = Math.floor(Math.random() * 256);
      return a;
    },
  };
}
