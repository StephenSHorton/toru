import Settings from "@/routes/Settings";
import Overlay from "@/routes/Overlay";
import Editor from "@/routes/Editor";
import Trim from "@/routes/Trim";
import EditVideo from "@/routes/EditVideo";
import Recording from "@/routes/Recording";
import RecFrame from "@/routes/RecFrame";
import Share from "@/routes/Share";
import Timeline from "@/routes/Timeline";
import Watch from "@/routes/Watch";
import Call from "@/routes/Call";

// Each Wails window opens with a `?view=` query param (see windows.go). We route
// on that rather than the path so the embedded SPA needs no server fallback.
// Toru is a tray app: the Settings/home window is the default (opened once on
// launch and from the tray); there is no dev Hub anymore.
export default function App() {
  const view = new URLSearchParams(window.location.search).get("view") ?? "settings";
  switch (view) {
    case "overlay":
      return <Overlay />;
    case "editor":
      return <Editor />;
    case "trim":
      return <Trim />;
    case "edit":
      return <EditVideo />;
    case "recording":
      return <Recording />;
    case "share":
      return <Share />;
    case "recframe":
      return <RecFrame />;
    case "timeline":
      return <Timeline />;
    case "watch":
      return <Watch />;
    case "call":
      return <Call />;
    case "settings":
    default:
      return <Settings />;
  }
}
