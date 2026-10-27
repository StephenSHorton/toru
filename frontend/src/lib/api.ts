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
//   UpdateService.GetVersion(): Promise<string>
//   UpdateService.CheckForUpdate(): Promise<UpdateInfo | null>  (null === up to date)
//   UpdateService.GetStatus(): Promise<UpdateStatus>  (phase idle|checking|downloading|ready|installing|error; "update:status" events)
//   UpdateService.CheckAndDownload(): Promise<UpdateStatus>  (manual check: downloads + verifies, never installs)
//   UpdateService.RestartToUpdate(): Promise<void>  (phase ready only: launches the installer and quits)
//   HotkeyService.GetShortcuts(): Promise<Shortcut[]>
//   HotkeyService.SetShortcut(action: string, sc: Shortcut): Promise<void>  (rejects on validation error)
//   HotkeyService.ResetShortcut(action: string): Promise<void>
//   HotkeyService.RecordShortcut(): Promise<Shortcut>  (next combo pressed; key "" = cancelled)
//   HotkeyService.CancelRecording(): Promise<void>
//   WindowsService.OpenOverlay(): Promise<void>   (capture overlay session)
//   WindowsService.OpenSettings(): Promise<void>  (Settings/home window)
//   WindowsService.OpenTrim(videoPath): Promise<void>  (Dev 2 trim editor)
//   VideoService.Trim(req: TrimRequest): Promise<string>  (returns outPath)
//   SettingsService.GetLaunchAtLogin(): Promise<boolean>
//   SettingsService.SetLaunchAtLogin(enabled: boolean): Promise<void>  (registry Run key; --startup => silent-to-tray)
//   HistoryService.Add(srcPath, kind): Promise<Item>  (archive file into library)
//   HistoryService.Delete(id): Promise<void>  (one item + file; Library bulk-delete loops this)
//   HistoryService.GetDir / SetDir / PickDir / ResetDir / IsDefaultDir
//   ReplayService.GetSettings / SetSettings(s) -> ReplayStatus  (instant replay; SetSettings rejects if the buffer can't start)
//   ReplayService.GetStatus / SaveReplay / ListMonitors / ListMicrophones / DiscordRunning / EncoderSupport
//   ReplayService.GetSaveDir / PickSaveDir / ResetSaveDir / OpenSaveDir
//   ReplayService.ListClips / OpenClip(path) / RevealClip(path) / DeleteClip(path)  (Library "Clips")
//   UploadService.ShareLink(path, startMs, endMs) -> public catbox link; "upload:progress" events; CancelShare / LinkFor(path)
//   TransferService.StartSend(path) / StopSend() / SendStatus(): Promise<SendState>   (progress on "transfer:send")
//   TransferService.StartReceive(code) / CancelReceive() / ReceiveStatus(): Promise<ReceiveState>  ("transfer:receive")
//   TransferService.PickSendFile / PickSendFolder(): Promise<string>  ("" === cancelled; does NOT start the send)
//   TransferService.GetReceiveDir / IsDefaultReceiveDir / PickReceiveDir / ResetReceiveDir / OpenReceiveDir / RevealReceived
//   TransferService.CodeQR(code): Promise<string>  (base64 PNG, no data: prefix)
//   ClickerService.GetSettings / SetSettings(s) -> normalized ClickerSettings / ResetSettings  ("clicker:settings" when Go changes them)
//   ClickerService.Status / Start (page countdown) / Stop / Toggle / SetPaused / SetArmed  ("clicker:status")
//   ClickerService.StartPointPick / StartZonePick(id|"") / CancelPick / PreviewSafety / CursorPosition  ("clicker:pick")
//   ClickerService.ListProcesses / SavePreset / ApplyPreset / UpdatePreset / RenamePreset / DuplicatePreset / DeletePreset / ExportPreset / ImportPreset / Stats / ResetStats
//   Errors read "clicker: <CODE>: message" (codes in internal/clicker/types.go).
import * as ExportService from "../../bindings/github.com/StephenSHorton/toru/internal/export/exportservice.js";
import * as ScreenshotService from "../../bindings/github.com/StephenSHorton/toru/internal/shot/screenshotservice.js";
import * as OverlayService from "../../bindings/github.com/StephenSHorton/toru/internal/overlay/overlayservice.js";
import * as UpdateService from "../../bindings/github.com/StephenSHorton/toru/internal/update/updateservice.js";
import {
  UpdateInfo,
  Status as UpdateStatus,
} from "../../bindings/github.com/StephenSHorton/toru/internal/update/models.js";
import * as HotkeyService from "../../bindings/github.com/StephenSHorton/toru/internal/hotkey/hotkeyservice.js";
import { Shortcut } from "../../bindings/github.com/StephenSHorton/toru/internal/hotkey/models.js";
import * as WindowsService from "../../bindings/github.com/StephenSHorton/toru/windowsservice.js";
import * as VideoService from "../../bindings/github.com/StephenSHorton/toru/internal/vid/videoservice.js";
import {
  TrimRequest,
  AudioConfig,
  AudioSession,
  ReplayStatus,
} from "../../bindings/github.com/StephenSHorton/toru/internal/capture/models.js";
import * as SettingsService from "../../bindings/github.com/StephenSHorton/toru/settingsservice.js";
import * as HistoryService from "../../bindings/github.com/StephenSHorton/toru/internal/history/historyservice.js";
import {
  Item as CaptureItem,
  Retention as LibraryRetention,
} from "../../bindings/github.com/StephenSHorton/toru/internal/history/models.js";
import * as UploadService from "../../bindings/github.com/StephenSHorton/toru/internal/upload/uploadservice.js";
import * as ReplayService from "../../bindings/github.com/StephenSHorton/toru/internal/replay/replayservice.js";
import {
  Settings as ReplaySettings,
  Monitor as ReplayMonitor,
  Clip as ReplayClip,
  Session as RecordingSession,
  SessionMarker as RecordingMarker,
  RecorderState,
  RecordingPreset,
  FFmpegState,
} from "../../bindings/github.com/StephenSHorton/toru/internal/replay/models.js";
import * as TransferService from "../../bindings/github.com/StephenSHorton/toru/internal/transfer/transferservice.js";
import {
  SendState,
  ReceiveState,
} from "../../bindings/github.com/StephenSHorton/toru/internal/transfer/models.js";
import * as ClickerService from "../../bindings/github.com/StephenSHorton/toru/internal/clicker/clickerservice.js";
import {
  Settings as ClickerSettings,
  Status as ClickerStatus,
  Profile as ClickerProfile,
  ClickPoint,
  StopZone,
  Preset as ClickerPreset,
  ProcessInfo,
  ProcessEntry,
  Stats as ClickerStats,
  PickEvent,
  Point as ClickerPoint,
} from "../../bindings/github.com/StephenSHorton/toru/internal/clicker/models.js";

export {
  ExportService,
  ScreenshotService,
  OverlayService,
  UpdateService,
  UpdateInfo,
  UpdateStatus,
  HotkeyService,
  Shortcut,
  WindowsService,
  VideoService,
  TrimRequest,
  AudioConfig,
  AudioSession,
  SettingsService,
  HistoryService,
  LibraryRetention,
  ReplayService,
  UploadService,
  ReplaySettings,
  ReplayMonitor,
  ReplayClip,
  RecordingSession,
  RecordingMarker,
  RecorderState,
  RecordingPreset,
  FFmpegState,
  ReplayStatus,
  TransferService,
  SendState,
  ReceiveState,
  ClickerService,
  ClickerSettings,
  ClickerStatus,
  ClickerProfile,
  ClickPoint,
  StopZone,
  ClickerPreset,
  ProcessInfo,
  ProcessEntry,
  ClickerStats,
  PickEvent,
  ClickerPoint,
};
export type { CaptureItem };
