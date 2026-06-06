import Hub from "@/routes/Hub";
import Overlay from "@/routes/Overlay";
import Editor from "@/routes/Editor";
import Trim from "@/routes/Trim";
import Recording from "@/routes/Recording";

// Each Wails window opens with a `?view=` query param (see windows.go). We route
// on that rather than the path so the embedded SPA needs no server fallback.
export default function App() {
  const view = new URLSearchParams(window.location.search).get("view") ?? "hub";
  switch (view) {
    case "overlay":
      return <Overlay />;
    case "editor":
      return <Editor />;
    case "trim":
      return <Trim />;
    case "recording":
      return <Recording />;
    default:
      return <Hub />;
  }
}
