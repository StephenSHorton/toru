// migration-golden holds the v2 → v3 migration to v2's pixels. The 12 v2
// clips in internal/vid/effects_parity_test.go (migrationCases) were drawn
// once through the v2 export chain (ClipVideoFilter) with the pinned LGPL
// ffmpeg; those reference PNGs live in internal/vid/testdata/migration with
// the source frame and golden.json (each clip's migrated stack). This
// script checks:
//   - migrateClipEffects (src/trim/migrate.ts) still gives the recorded
//     stacks, so a change to the migration is deliberate (rewrite the
//     goldens with --write after reviewing it);
//   - the preview's renderer draws each migrated stack within v2's limits
//     (mean ≤ 1, p99 ≤ 6) of the reference.
// TestMigrationGoldenExport (Go) does the same for the v3 export.
//
//   bun scripts/migration-golden.ts           check
//   bun scripts/migration-golden.ts --write   redraw the references (pinned
//                                             ffmpeg) and record the stacks
import fs from "node:fs";
import path from "node:path";
import { compare, ffmpegFor, goBin, openPage, RENDER_PAGE, repo, run, tempDir } from "./fx-edge";

const DIR = path.join(repo, "internal/vid/testdata/migration");
const SRC = path.join(DIR, "src.png");
const W = 320;
const LIMITS = { mode: "pixels" as const, mean: 1, p99: 6 };

type V2Case = { name: string; chroma?: unknown; filters: unknown; filter: string; border: number };
type Golden = { height: number; cases: { name: string; chroma?: unknown; filters: unknown; effects: unknown[]; png: string; border: number }[] };

const tmp = tempDir("fx-migration");
const write = process.argv.includes("--write");
const ffmpeg = ffmpegFor(tmp);
const rgba = (png: string) => run(ffmpeg, ["-v", "error", "-i", png, "-f", "rawvideo", "-pix_fmt", "rgba", "-"]);

let failed = 0;
try {
  if (write) {
    run(goBin, ["test", "./internal/vid/", "-run", "^TestWriteMigrationCases$", "-count=1"], { TORU_MIGRATION_DIR: tmp });
    const { height: H, cases } = JSON.parse(fs.readFileSync(path.join(tmp, "cases.json"), "utf8")) as { height: number; cases: V2Case[] };
    fs.rmSync(DIR, { recursive: true, force: true });
    fs.mkdirSync(DIR, { recursive: true });
    // The parity script's frame: test pattern, green, near-green, white and
    // black boxes.
    run(ffmpeg, [
      "-v", "error", "-y", "-f", "lavfi", "-i", `testsrc2=s=${W}x${H}:r=1:d=1`,
      "-vf", "drawbox=x=40:y=30:w=110:h=80:c=0x00FF00:t=fill,drawbox=x=170:y=40:w=70:h=60:c=0x3CC850:t=fill," +
        "drawbox=x=250:y=120:w=50:h=40:c=0xFFFFFF:t=fill,drawbox=x=40:y=125:w=60:h=40:c=0x000000:t=fill",
      "-frames:v", "1", SRC,
    ]);
    const page = await openPage(tmp, RENDER_PAGE, {});
    const golden: Golden = { height: H, cases: [] };
    try {
      for (const [i, c] of cases.entries()) {
        const png = `${String(i + 1).padStart(2, "0")}.png`;
        run(ffmpeg, ["-v", "error", "-y", "-i", SRC, "-vf", (c.filter ? c.filter + "," : "") + "format=rgba", "-frames:v", "1", "-pix_fmt", "rgba", path.join(DIR, png)]);
        const effects = await page.evaluate(`migrate(${JSON.stringify(c.chroma ?? null)}, ${JSON.stringify(c.filters)})`);
        golden.cases.push({ name: c.name, chroma: c.chroma, filters: c.filters, effects, png, border: c.border });
      }
    } finally {
      await page.close();
    }
    // One case per line keeps the file short and its diffs readable.
    fs.writeFileSync(path.join(DIR, "golden.json"), `{"height":${H},"note":"frontend/scripts/migration-golden.ts --write regenerates this","cases":[\n${golden.cases.map((c) => JSON.stringify(c)).join(",\n")}\n]}\n`);
    console.log(`wrote ${golden.cases.length} references to ${DIR}`);
  }

  const golden = JSON.parse(fs.readFileSync(path.join(DIR, "golden.json"), "utf8")) as Golden;
  const H = golden.height;
  const page = await openPage(tmp, RENDER_PAGE, { "/src": rgba(SRC) });
  try {
    console.log(`migration goldens, ${W}x${H}, limits mean <= ${LIMITS.mean}, p99 <= ${LIMITS.p99}`);
    for (const c of golden.cases) {
      const effects = await page.evaluate(`migrate(${JSON.stringify(c.chroma ?? null)}, ${JSON.stringify(c.filters)})`);
      // Ids are fresh per migration; the rest must match.
      const strip = (es: any[]) => JSON.stringify(es.map(({ id: _, ...e }) => e));
      if (strip(effects) !== strip(c.effects)) {
        failed++;
        console.log(`FAIL ${c.name.padEnd(36)} migrates to ${strip(effects)}\n     recorded ${strip(c.effects)} (run with --write if the change is meant)`);
        continue;
      }
      const got = await page.evaluate(`render(${W}, ${H}, ${JSON.stringify({ effects })})`);
      const r = compare(rgba(path.join(DIR, c.png)), Buffer.from(got.pixels, "base64"), W, H, c.border, LIMITS);
      if (!r.ok) failed++;
      console.log(`${r.ok ? "ok  " : "FAIL"} ${c.name.padEnd(36)} mean ${r.mean.toFixed(2).padStart(5)}  p99 ${r.p99.toFixed(1).padStart(5)}  max ${r.max.toFixed(1).padStart(5)}`);
    }
  } finally {
    await page.close();
  }
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}
console.log(failed ? `${failed} case(s) failed` : "all migrated clips match v2");
process.exit(failed ? 1 : 0);
