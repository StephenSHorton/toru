// Foundation-owned re-export of the Go->JS bindings. ALL editor backend calls
// import from "@/lib/api" — never from the raw bindings path — so there is one
// stable seam if the generated layout shifts.
//
// Shapes (from the generated bindings + service.go):
//   ExportService.CopyToClipboard(path: string, mediaType: "image"|"video"): Promise<void>
//   ExportService.SaveAs(srcPath: string, suggestedName: string): Promise<string>  ("" === cancelled)
//   ExportService.ReadClipboardImage(): Promise<string>   (data URL, or "" if none)
//   ScreenshotService.SavePNG(pngBase64: string): Promise<string>  (accepts full data: URL, returns temp path)
//   OverlayService.* (capture/recording coordination — exposed for completeness)
import * as ExportService from "../../bindings/github.com/StephenSHorton/toru/internal/export/exportservice.js";
import * as ScreenshotService from "../../bindings/github.com/StephenSHorton/toru/internal/shot/screenshotservice.js";
import * as OverlayService from "../../bindings/github.com/StephenSHorton/toru/internal/overlay/overlayservice.js";

export { ExportService, ScreenshotService, OverlayService };
