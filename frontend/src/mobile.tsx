import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "highlight.js/styles/github.css";
import "./nozoom";
import "./theme.css";

// The phone client's Chat page: the same <App/> the desktop WebView renders,
// without the Projects and Files routes. The host (solco-mobile) injects a
// `globalThis.mygo` shim before this script runs, so the generated bridge in
// ./mygo.ts talks to the Host through React Native instead of a MyGo window.
document.documentElement.dataset.host = "phone";

// The host names the page it embeds before this script runs (`__SOLCO_ROUTE__`, like the desktop's pathname):
// the Chat body by default, the Feedback inbox for the phone's Feedback screen.
const FeedbackPage = React.lazy(() =>
  import("./feedback/FeedbackPage").then((m) => ({ default: m.FeedbackPage })),
);
const route = (globalThis as { __SOLCO_ROUTE__?: string }).__SOLCO_ROUTE__;

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    {route === "feedback" ? (
      <React.Suspense fallback={<div className="fb-boot" />}>
        <FeedbackPage />
      </React.Suspense>
    ) : (
      <App />
    )}
  </React.StrictMode>,
);
