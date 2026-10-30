// Solco app + frontend configuration (N19 Phase 0). This file replaced
// mygo.json when the chat WebView frontend arrived: mygo.config.ts and
// mygo.json configure the same project and are mutually exclusive, so the
// packaging identity moved here next to the frontend keys. During
// `go tool mygo dev` the chat page is served by Vite with hot reload;
// `go tool mygo build` embeds frontend/dist behind mygo://localhost, and
// `mygo generate` writes the typed bridge client to frontend/src/mygo.ts.
export default {
  name: "Solco",
  identifier: "com.whstudio.solco",
  version: "0.10.0",
  // solco://reveal?session=&workspace=&tab= — see internal/nativeui/external_reveal.go.
  urlSchemes: ["solco"],
  out: "build",
  icon: "resources/icon.png",

  devUrl: "http://localhost:5173",
  devCommand: "bun run --cwd frontend dev",
  buildCommand:
    "bun install --cwd frontend --frozen-lockfile && bun run --cwd frontend build",
  frontendDist: "frontend/dist",
  bindings: "frontend/src/mygo.ts",

  updates: {
    publicKey: "MqdM0/CqkQ2NmX0RpMrNm1CzCnUXLt55Z/bZq+zhk8w=",
    github: "thewei/solco",
  },

  macos: {
    minimumSystemVersion: "13.0",
    infoPlist: { NSMicrophoneUsageDescription: "Solco uses the microphone for chat voice dictation." },
  },
};
