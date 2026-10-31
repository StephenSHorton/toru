// export-check exercises the export screen's maths (src/trim/exportSettings.ts):
// output sizes per resolution choice, the Go ExportOptions it sends, and the
// size estimate.
//
//   bun scripts/export-check.ts
import { DEFAULT_STATE, estimateBytes, outputSize, toOptions, type ExportState } from "../src/trim/exportSettings";

let pass = 0;
let fail = 0;
function eq(name: string, got: unknown, want: unknown) {
  const g = JSON.stringify(got);
  const w = JSON.stringify(want);
  if (g === w) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}: got ${g}, want ${w}`);
  }
}
const st = (p: Partial<ExportState>): ExportState => ({ ...DEFAULT_STATE, ...p });

// Sizes: presets never scale up, keep the aspect, stay even.
eq("original", outputSize(st({}), 3440, 1440), { w: 3440, h: 1440 });
eq("1080 of ultrawide", outputSize(st({ size: "1080" }), 3440, 1440), { w: 2580, h: 1080 });
eq("1080 of 720p keeps", outputSize(st({ size: "1080" }), 1280, 720), { w: 1280, h: 720 });
eq("720 odd width", outputSize(st({ size: "720" }), 1366, 768), { w: 1280, h: 720 });
eq("custom exact", outputSize(st({ size: "custom", width: 1080, height: 1920 }), 1920, 1080), { w: 1080, h: 1920 });
eq("gif width", outputSize(st({ format: "gif", gifWidth: 480 }), 1920, 1080), { w: 480, h: 270 });
eq("discord ignores size", outputSize(st({ discord: true, size: "720" }), 1920, 1080), { w: 1920, h: 1080 });

// Options sent to Go.
eq("default", toOptions(st({}), null, 1920, 1080), { format: "mp4", quality: "high", audioKbps: 160, sampleRate: 48000 });
eq("1080 on 1440 + range", toOptions(st({ size: "1080", fps: 30 }), { in: 1.25, out: 3.5 }, 2560, 1440), {
  format: "mp4",
  rangeIn: 1250,
  rangeOut: 3500,
  height: 1080,
  fps: 30,
  quality: "high",
  audioKbps: 160,
  sampleRate: 48000,
});
eq("1080 on 1080 sends no size", toOptions(st({ size: "1080" }), null, 1920, 1080).height, undefined);
eq(
  "custom fill hevc bitrate no audio",
  toOptions(st({ format: "hevc", size: "custom", width: 1080, height: 1920, fit: "fill", rateMode: "bitrate", kbps: 6000, audio: false, encoder: "hevc_nvenc" }), null, 1920, 1080),
  { format: "hevc", width: 1080, height: 1920, fit: "fill", kbps: 6000, encoder: "hevc_nvenc", noAudio: true },
);
eq("gif", toOptions(st({ format: "gif", gifWidth: 481, gifLoop: -1 }), null, 1920, 1080), {
  format: "gif",
  gif: { fps: 15, width: 482, loop: -1, dither: "sierra2_4a", colors: 256 },
});
eq("m4a", toOptions(st({ format: "m4a", audioKbps: 256, sampleRate: 44100 }), null, 1920, 1080), { format: "m4a", audioKbps: 256, sampleRate: 44100 });
eq("webm audio is 48k", toOptions(st({ format: "webm", sampleRate: 44100 }), null, 1920, 1080).sampleRate, 48000);
eq("discord keeps quality, not kbps", toOptions(st({ discord: true, rateMode: "bitrate" }), null, 1920, 1080).kbps, undefined);

// Estimates.
const br = estimateBytes(st({ rateMode: "bitrate", kbps: 8000, audioKbps: 160 }), 10, 1920, 1080, 60);
eq("bitrate exact", [Math.round(br.bytes), br.exact], [10_200_000, true]);
const q = estimateBytes(st({}), 10, 1920, 1080, 60);
eq("quality is an estimate", q.exact, false);
eq("hevc smaller than h264", estimateBytes(st({ format: "hevc" }), 10, 1920, 1080, 60).bytes < q.bytes, true);
eq("m4a", estimateBytes(st({ format: "m4a", audioKbps: 128 }), 8, 0, 0, 0).bytes, 128_000);
eq("discord under 10 MB", estimateBytes(st({ discord: true }), 600, 1920, 1080, 60).bytes <= 9.5e6, true);

console.log(`${pass} passed, ${fail} failed`);
if (fail) process.exit(1);
