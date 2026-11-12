import * as fs from 'node:fs';
import * as path from 'node:path';
import { BruiApp } from './ui/app';
import { ALL_THEMES } from './themes';

function showHelp() {
  console.log(`
\x1b[1m\x1b[35m⚡ BRUI\x1b[0m - Fast, mouse-friendly TUI for Bruno API collections

\x1b[1mUsage:\x1b[0m
  brui [collection-path] [options]

\x1b[1mOptions:\x1b[0m
  -e, --env <name>      Select active environment (e.g. --env SIT)
  -t, --theme <name>    Select initial theme (default: Catppuccin Mocha)
  -l, --list-themes     List all available themes
  -h, --help            Show this help message
  -v, --version         Show brui version

\x1b[1mKeybindings in TUI:\x1b[0m
  [Enter] / [Space]     Send selected request
  [O]                   Trigger OAuth2 browser login (PKCE & loopback)
  [E]                   Cycle environment
  [T]                   Cycle Catppuccin theme
  [1-7]                 Switch Request tab (Headers, Params, Body, Auth, etc.)
  [Tab]                 Switch focus between Sidebar and Content
  [Mouse Click]         Select request / click tabs
  [Mouse Wheel]         Scroll request & response view
  [q] / [Ctrl+C]        Quit

\x1b[1mAvailable Themes:\x1b[0m
  ${ALL_THEMES.map(t => t.name).join(', ')}
`);
  process.exit(0);
}

function showVersion() {
  try {
    const pkg = JSON.parse(fs.readFileSync(path.join(__dirname, '../package.json'), 'utf8'));
    console.log(`brui v${pkg.version}`);
  } catch {
    console.log('brui v0.1.0');
  }
  process.exit(0);
}

async function main() {
  const args = process.argv.slice(2);
  let collectionPath = process.cwd();
  let themeName = 'Catppuccin Mocha';
  let initialEnv: string | undefined = undefined;

  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === '-h' || arg === '--help') {
      showHelp();
    } else if (arg === '-v' || arg === '--version') {
      showVersion();
    } else if (arg === '-l' || arg === '--list-themes') {
      console.log('Available themes:');
      ALL_THEMES.forEach(t => console.log(`  - ${t.name}`));
      process.exit(0);
    } else if (arg === '-t' || arg === '--theme') {
      themeName = args[++i];
    } else if (arg === '-e' || arg === '--env') {
      initialEnv = args[++i];
    } else if (!arg.startsWith('-')) {
      collectionPath = arg;
    }
  }

  let resolvedPath = path.resolve(collectionPath);
  if (!fs.existsSync(resolvedPath)) {
    console.error(`\x1b[31mError: Path does not exist: ${resolvedPath}\x1b[0m`);
    process.exit(1);
  }

  // If the target path itself is not a collection, check if it contains collections
  const { CollectionParser } = await import('./core/parser');
  if (!CollectionParser.isBrunoCollection(resolvedPath)) {
    const discovered = CollectionParser.discoverCollections(resolvedPath);
    if (discovered.length > 0) {
      resolvedPath = discovered[0].path;
    } else {
      console.error(`\x1b[31mError: No Bruno collection found in: ${resolvedPath}\x1b[0m`);
      process.exit(1);
    }
  }

  try {
    const app = new BruiApp(resolvedPath, themeName);
    app.start();
  } catch (err: any) {
    console.error(`\x1b[31mError launching brui: ${err.message}\x1b[0m`);
    process.exit(1);
  }
}

main();
