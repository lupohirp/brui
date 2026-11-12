# ⚡ brui

> **The mouse-friendly, Catppuccin-themed Terminal API Client for Bruno collections.**
> Native Node.js scripting runtime, automatic nested request execution (`await bru.runRequest`), interactive OAuth2 with browser loopback & PKCE, and ultra-low RAM footprint (~35MB).

---

## ✨ Features

- 📁 **Native Bruno (.bru) Compatibility**: Reads and writes Bruno collections and environments directly using `@usebruno/lang`. Zero conversion or import needed.
- 🖱️ **Full Mouse Support**: Click to navigate the collection tree, click tabs (Headers, Params, Body, Auth, Pre/Post Scripts, Docs), and scroll request/response views with your mouse wheel.
- 🎨 **Catppuccin & Modern Themes**: Built-in support for **Catppuccin Mocha**, **Macchiato**, **Frappé**, **Latte**, plus **Tokyo Night** and **Dracula**. Switch themes on-the-fly with `T`.
- ⚡ **True Node.js Scripting Engine**: Pre-request and post-response scripts run inside an isolated `node:vm` context with access to the collection's local `node_modules` (you can `require()` any installed npm package!).
- 🔄 **Automatic Request Nesting (`await bru.runRequest`)**: Supports automatic token acquisition and renewal (e.g. running an `_Auth/Get Token` request before calling an API when the token is missing or expired).
- 🌐 **Interactive OAuth2 with Browser Loopback**:
  - Automatically spins up a local temporary HTTP listener on `http://127.0.0.1:3000/callback`.
  - Opens your system's default browser with PKCE (`S256`).
  - Intercepts the authorization code, performs the token exchange, and stores the token in the active environment.
- 🪶 **Low Memory Consumption**: Consumes ~35MB RAM, starting up instantaneously without Electron or Chromium bloat.

---

## 🚀 Quick Start

### Installation

```bash
# Clone the repository
git clone https://github.com/pakilodi/brui.git
cd brui

# Install dependencies and build
npm install
npm run build

# (Optional) Link globally so you can use 'brui' anywhere
npm link
```

### Usage

```bash
# Launch on a Bruno collection directory
brui /path/to/bruno-collection

# Launch with a specific theme
brui /path/to/bruno-collection --theme "Catppuccin Mocha"

# Launch with a specific environment pre-selected
brui /path/to/bruno-collection --env SIT
```

---

## ⌨️ Controls & Keybindings

| Key / Action | Description |
| :--- | :--- |
| **`Enter`** / **`Space`** | Send the selected request |
| **`O`** | Trigger OAuth2 interactive browser login |
| **`E`** | Cycle active environment (e.g. `SIT` → `EVO` → `GREEN`) |
| **`T`** | Cycle themes (Mocha, Macchiato, Frappé, Latte, Tokyo Night, Dracula) |
| **`1` – `7`** | Switch request tabs (Headers, Params, Body, Auth, Pre-Script, Post-Script, Docs) |
| **`Tab`** | Switch keyboard focus between Sidebar and Content panels |
| **`q`** / **`Ctrl+C`** | Quit brui |
| **`Mouse Click`** | Select any request in the sidebar or click any tab |
| **`Mouse Wheel`** | Scroll response body, headers, or request content |

---

## 🏗️ Architecture

```
src/
├── core/
│   ├── parser.ts          # Uses @usebruno/lang to parse .bru and environments
│   ├── interpolation.ts   # Resolves {{variables}} from runtime, env, and system
│   ├── http-client.ts     # Native Node fetch client with timing and headers
│   ├── script-runner.ts   # Node.js VM runtime (bru, req, res, expect, require)
│   └── oauth2.ts          # Loopback HTTP server & browser opening (PKCE + code exchange)
├── themes/
│   └── index.ts           # Catppuccin and community color palettes
├── ui/
│   └── app.ts             # Neo-Blessed terminal UI orchestrator with mouse events
├── types.ts               # Shared TypeScript definitions
└── index.ts               # CLI argument parser & launcher
```

---

## 📄 License

MIT © Paolo Chilodi
