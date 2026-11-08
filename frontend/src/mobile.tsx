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
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
