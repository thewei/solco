# site/
> L2 | Parent: /CLAUDE.md

index.html: the GitHub Pages entry page. It keeps the ram icon in the navbar only, presents one real-app hero screenshot, groups features into Agents/Sessions, Terminal/Files, and Git/Devices with masked top-left screenshot crops, explains the Solco-to-Herdr ownership boundary, and points macOS users to GitHub Releases.
styles.css: the single presentation stylesheet. It defines the light gray palette, Apple-style typography scale, sticky navbar, feature crop masks, responsive layout, and the footer. It has no JavaScript dependencies.
assets/screens/product-hero.webp: full-app hero screenshot processed from the real Solco window.
assets/screens/feature-agents.webp: top-left feature crop for sessions and agent context.
assets/screens/feature-terminal.webp: top-left feature crop for terminal work.
assets/screens/feature-files.webp: secondary top-left crop for the Files view.
assets/screens/feature-git.webp: top-left feature crop for Git review.
assets/screens/feature-hosts.webp: secondary top-left crop for saved machines and devices.
assets/appicon/: responsive brand exports. The navbar, favicon, Apple touch icon, Open Graph image, and footer use these files; `solco-1024.png` is the runtime copy of `resources/icon.png`. The approved prompt and Apple safe-margin export rule live in `assets/app-icon/PROMPT.md`.
assets/backends/herdr-192.png: Herdr brand reference used by site design assets; it is not a second Solco mark.
assets/backends/luvus-192.png: Luvus brand reference retained with the site asset package.
assets/backends/tmux.svg: tmux brand reference retained with the site asset package.
assets/backends/uu.png: UU Remote brand reference retained with the site asset package.
CLAUDE.md: this file.

[PROTOCOL]: Update this header on change, then check CLAUDE.md.
