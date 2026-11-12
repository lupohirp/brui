# brui

Terminal user interface (TUI) for [Bruno](https://www.usebruno.com/) API collections.

`brui` reads and writes `.bru` files directly using `@usebruno/lang`, executes pre-request and post-response scripts in a Node.js `vm` context, decrypts local Bruno `secrets.json` stores using `@usebruno/node-machine-id`, and handles OAuth2 token flows (`password`, `client_credentials`, and `authorization_code` with PKCE).

## Features

- **Direct `.bru` file support**: Parses and modifies collections, folders, requests, and environments in place via `@usebruno/lang`.
- **Environment & Secret Management**: Reads plaintext variables from `environments/*.bru` and decrypts secret variables stored in Bruno's local `~/.config/bruno/secrets.json`. Allows viewing, editing, and persisting variables back to disk.
- **Authentication Manager**: Configure `inherit`, `none`, `bearer`, `basic`, or `oauth2` per request and save changes directly to the `.bru` file.
- **OAuth2 Support**:
  - `password` grant (Resource Owner Password Credentials)
  - `client_credentials` grant
  - `authorization_code` grant with PKCE (`S256`) and local loopback listener (`http://127.0.0.1:3000/callback`)
- **Scripting Runtime**: Runs `script:pre-request`, `script:post-response`, and `tests` blocks with support for `bru.getEnvVar`, `bru.setEnvVar`, `bru.getVar`, `bru.setVar`, `await bru.runRequest`, `req`, `res`, and `expect`.
- **Keyboard & Mouse Navigation**: Full mouse click/scroll support alongside keyboard shortcuts.
- **Themes**: Catppuccin (Mocha, Macchiato, Frappe, Latte), Tokyo Night, and Dracula.

## Installation

```bash
git clone https://github.com/lupohirp/brui.git
cd brui
npm install
npm run build
npm link
```

## Usage

```bash
# Open a Bruno collection directory (defaults to current directory)
brui /path/to/collection

# Pre-select an environment
brui /path/to/collection --env SIT

# Select a theme
brui /path/to/collection --theme "Catppuccin Mocha"

# List available themes
brui --list-themes
```

## Keybindings

| Key | Action |
| :--- | :--- |
| `Space` / `Enter` | Send selected request (or toggle folder in sidebar) |
| `C` | Open collection switcher |
| `E` | Open environment and variables manager |
| `A` | Open authentication manager for current request |
| `O` | Acquire or renew OAuth2 access token |
| `M` | Cycle authentication mode (when on Auth tab) |
| `S` | Save authentication settings to `.bru` file |
| `T` | Cycle color theme |
| `1` - `7` | Switch request tab (`Headers`, `Params`, `Body`, `Auth`, `Pre-Script`, `Post-Script`, `Docs`) |
| `Tab` | Cycle focus between sidebar, request pane, and response pane |
| `q` / `Ctrl+C` | Exit |

## Project Structure

```text
src/
├── core/
│   ├── http-client.ts     # HTTP request execution and timing
│   ├── interpolation.ts   # {{variable}} resolution across runtime, env, and process.env
│   ├── oauth2.ts          # OAuth2 password, client_credentials, and PKCE loopback flows
│   ├── parser.ts          # .bru collection/environment parser and Bruno secrets.json decryption
│   └── script-runner.ts   # Node.js VM sandbox for pre/post scripts and bru.runRequest
├── themes/
│   └── index.ts           # Color palettes
├── ui/
│   ├── app.ts             # Main TUI layout and event handling
│   ├── auth-manager.ts    # Interactive authentication configuration modal
│   └── env-manager.ts     # Environment selector and variable editor modal
├── types.ts               # TypeScript interfaces
└── index.ts               # CLI entry point
```

## License

MIT
