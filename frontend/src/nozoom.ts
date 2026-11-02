/**
 * Pinch zoom off: these pages are app UI inside a native window (Solco's
 * desktop WebView and the phone's WebView alike), and a page that magnifies
 * under a trackpad pinch is not native. WebKit delivers a pinch as
 * gesture events (gesturestart/gesturechange — Safari's and an embedded
 * WKWebView's on macOS and iOS) whose default action zooms the page;
 * Chromium-style engines send it as a wheel event with Ctrl held. Taking
 * the events cancels the zoom everywhere; plain scrolling, ⌘-shortcuts and
 * element-level gesture handling registered before this module all keep
 * working. Imported for its side effect by both page entries.
 */
for (const type of ["gesturestart", "gesturechange", "gestureend"] as const) {
  window.addEventListener(type, (e) => e.preventDefault());
}
window.addEventListener(
  "wheel",
  (e) => {
    if (e.ctrlKey) e.preventDefault();
  },
  { passive: false },
);
