import React from "react";
import ReactDOM from "react-dom/client";
import "./index.css";
import App from "./App";

// The overlay + recframe windows are TRANSPARENT (they show the live desktop /
// recorded content through them). index.css paints an opaque app background on
// <body>, which would block the show-through. Null it out HERE — before the first
// React render and paint — so a transparent window never flashes an opaque frame.
// This MUST run pre-paint: doing it in a route useEffect (as the overlay also does
// for belt-and-suspenders) races the first composite, and for the recframe window
// that opaque first frame would be CAPTURED by ffmpeg (a dark flash baked into the
// recording, since the window sits over the recorded region). The overlay dodges
// that via Hidden+ACK, but recframe is shown immediately, so pre-paint is required.
{
  const view = new URLSearchParams(window.location.search).get("view");
  if (view === "overlay" || view === "recframe") {
    document.documentElement.style.background = "transparent";
    document.body.style.background = "transparent";
    document.body.style.backgroundImage = "none";
  }
}

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
