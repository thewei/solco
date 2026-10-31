import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

// The phone's Chat page: one self-contained HTML (inline JS/CSS) that
// solco-mobile embeds as a string and loads offline in react-native-webview
// (or an iframe on Web). Built by `bun run build:mobile`; consumed by
// `pnpm sync:chat-page` in solco-mobile. Android WebView is Chromium, so the
// target is lower than the desktop's Safari 15 only where esbuild needs it.
export default defineConfig({
  plugins: [react(), viteSingleFile()],
  build: {
    outDir: "dist-mobile",
    target: ["chrome90", "safari15"],
    rollupOptions: { input: "mobile.html" },
  },
});
