import blessed from 'neo-blessed';
import { BrunoCollection, BrunoRequest, HttpResponse, ConsoleLogEntry, ThemeColors } from '../types';
import { ThemeManager } from '../themes';
import { CollectionParser } from '../core/parser';
import { HttpClient } from '../core/http-client';
import { ScriptRunner } from '../core/script-runner';
import { OAuth2Handler } from '../core/oauth2';
import { Interpolator } from '../core/interpolation';

export class BruiApp {
  private collection: BrunoCollection;
  private parser: CollectionParser;
  private themeManager: ThemeManager;
  private scriptRunner: ScriptRunner;

  private allRequests: BrunoRequest[] = [];
  private selectedRequestIndex: number = 0;
  private activeRequestTab: 'headers' | 'params' | 'body' | 'auth' | 'pre-script' | 'post-script' | 'docs' = 'headers';
  private activeResponseTab: 'body' | 'headers' | 'console' = 'body';
  private lastResponse?: HttpResponse;

  // UI Widgets
  private screen!: blessed.Widgets.Screen;
  private headerBox!: blessed.Widgets.BoxElement;
  private sidebarList!: blessed.Widgets.ListElement;
  private requestPane!: blessed.Widgets.BoxElement;
  private requestTabsBox!: blessed.Widgets.BoxElement;
  private requestContentBox!: blessed.Widgets.BoxElement;
  private responsePane!: blessed.Widgets.BoxElement;
  private responseHeaderBox!: blessed.Widgets.BoxElement;
  private responseTabsBox!: blessed.Widgets.BoxElement;
  private responseContentBox!: blessed.Widgets.BoxElement;
  private statusBar!: blessed.Widgets.BoxElement;

  private isRunning: boolean = false;

  constructor(collectionPath: string, themeName: string = 'Catppuccin Mocha') {
    this.parser = new CollectionParser(collectionPath);
    this.collection = this.parser.parse();
    this.themeManager = new ThemeManager(themeName);
    this.scriptRunner = new ScriptRunner(this.collection, this.parser.requestMap);

    this.flattenRequests(this.collection.folders, this.collection.requests);
  }

  private flattenRequests(folders: any[], requests: BrunoRequest[]) {
    for (const r of requests) {
      this.allRequests.push(r);
    }
    for (const f of folders) {
      this.flattenRequests(f.folders, f.requests);
    }
  }

  public start() {
    this.screen = blessed.screen({
      smartCSR: true,
      title: `brui - ${this.collection.name}`,
      mouse: true,
      sendFocus: true,
      useBCE: true,
      cursor: {
        artificial: true,
        shape: 'line',
        blink: true,
        color: 'white'
      }
    });

    this.buildLayout();
    this.applyTheme();
    this.bindEvents();

    this.updateRequestView();
    this.updateResponseView();

    this.sidebarList.focus();
    this.screen.render();
  }

  private getTheme(): ThemeColors {
    return this.themeManager.getCurrent();
  }

  private buildLayout() {
    const theme = this.getTheme();

    // 1. Top Header (Height 3)
    this.headerBox = blessed.box({
      parent: this.screen,
      top: 0,
      left: 0,
      width: '100%',
      height: 3,
      tags: true,
      border: { type: 'line' },
      mouse: true
    });

    // 2. Left Sidebar (Requests list)
    this.sidebarList = blessed.list({
      parent: this.screen,
      top: 3,
      left: 0,
      width: '32%',
      bottom: 2,
      border: { type: 'line' },
      tags: true,
      mouse: true,
      keys: true,
      vi: true,
      scrollable: true,
      scrollbar: {
        ch: ' ',
        track: { bg: theme.surface },
        style: { inverse: true }
      }
    });

    // 3. Request Pane (Top Right)
    this.requestPane = blessed.box({
      parent: this.screen,
      top: 3,
      left: '32%',
      width: '68%',
      height: '48%',
      border: { type: 'line' },
      tags: true,
      mouse: true
    });

    this.requestTabsBox = blessed.box({
      parent: this.requestPane,
      top: 2,
      left: 1,
      right: 1,
      height: 1,
      tags: true,
      mouse: true
    });

    this.requestContentBox = blessed.box({
      parent: this.requestPane,
      top: 4,
      left: 1,
      right: 1,
      bottom: 1,
      tags: true,
      mouse: true,
      scrollable: true,
      alwaysScroll: true,
      scrollbar: {
        ch: ' ',
        track: { bg: theme.surface },
        style: { inverse: true }
      }
    });

    // 4. Response Pane (Bottom Right)
    this.responsePane = blessed.box({
      parent: this.screen,
      top: '51%',
      left: '32%',
      width: '68%',
      bottom: 2,
      border: { type: 'line' },
      tags: true,
      mouse: true
    });

    this.responseHeaderBox = blessed.box({
      parent: this.responsePane,
      top: 0,
      left: 1,
      right: 1,
      height: 2,
      tags: true,
      mouse: true
    });

    this.responseTabsBox = blessed.box({
      parent: this.responsePane,
      top: 2,
      left: 1,
      right: 1,
      height: 1,
      tags: true,
      mouse: true
    });

    this.responseContentBox = blessed.box({
      parent: this.responsePane,
      top: 4,
      left: 1,
      right: 1,
      bottom: 1,
      tags: true,
      mouse: true,
      scrollable: true,
      alwaysScroll: true,
      scrollbar: {
        ch: ' ',
        track: { bg: theme.surface },
        style: { inverse: true }
      }
    });

    // 5. Status Bar (Bottom)
    this.statusBar = blessed.box({
      parent: this.screen,
      bottom: 0,
      left: 0,
      width: '100%',
      height: 2,
      tags: true,
      mouse: true
    });

    this.populateSidebar();
  }

  private applyTheme() {
    const t = this.getTheme();

    // Screen
    this.screen.program.bg(t.bg);
    this.screen.program.fg(t.fg);

    // Header
    this.headerBox.style.bg = t.bg;
    this.headerBox.style.fg = t.fg;
    this.headerBox.style.border.fg = t.border;

    // Sidebar
    this.sidebarList.style.bg = t.bg;
    this.sidebarList.style.fg = t.fg;
    this.sidebarList.style.border.fg = t.borderFocus;
    this.sidebarList.style.selected = {
      bg: t.selection,
      fg: t.selectionFg,
      bold: true
    };
    this.sidebarList.style.item = {
      hover: { bg: t.surfaceHover }
    };

    // Request Pane
    this.requestPane.style.bg = t.bg;
    this.requestPane.style.fg = t.fg;
    this.requestPane.style.border.fg = t.border;
    this.requestContentBox.style.bg = t.surface;
    this.requestContentBox.style.fg = t.fg;

    // Response Pane
    this.responsePane.style.bg = t.bg;
    this.responsePane.style.fg = t.fg;
    this.responsePane.style.border.fg = t.border;
    this.responseContentBox.style.bg = t.surface;
    this.responseContentBox.style.fg = t.fg;

    // Status Bar
    this.statusBar.style.bg = t.surface;
    this.statusBar.style.fg = t.fg;

    this.updateHeader();
    this.updateStatusBar('Ready');
    this.screen.render();
  }

  private updateHeader() {
    const t = this.getTheme();
    const envName = this.collection.activeEnvironment || 'None';
    const authStatus = this.collection.runtimeVariables['access_token']
      ? `{${t.get}-fg}● Authenticated{/}`
      : `{${t.subtext}-fg}○ Not Authenticated{/}`;

    this.headerBox.setContent(
      ` {bold}{${t.accent}-fg}⚡ BRUI{/}  ` +
      `{${t.subtext}-fg}Collection:{/} {bold}${this.collection.name}{/}  ` +
      `{${t.subtext}-fg}Env:{/} {bold}{${t.options}-fg}[${envName}]{/}  ` +
      `{${t.subtext}-fg}Auth:{/} ${authStatus}  ` +
      `{right}{${t.subtext}-fg}Theme:{/} {${t.put}-fg}${t.name}{/} `
    );
  }

  private populateSidebar() {
    const t = this.getTheme();
    const items: string[] = [];

    for (const req of this.allRequests) {
      let badgeColor = t.get;
      if (req.method === 'POST') badgeColor = t.post;
      else if (req.method === 'PUT') badgeColor = t.put;
      else if (req.method === 'PATCH') badgeColor = t.patch;
      else if (req.method === 'DELETE') badgeColor = t.delete;

      const formatted = `{bold}{${badgeColor}-fg}${req.method.padEnd(6)}{/} ${req.name}`;
      items.push(formatted);
    }

    this.sidebarList.setItems(items);
  }

  private updateRequestView() {
    const t = this.getTheme();
    const req = this.allRequests[this.selectedRequestIndex];
    if (!req) {
      this.requestPane.setContent(' No request selected.');
      return;
    }

    let badgeColor = t.get;
    if (req.method === 'POST') badgeColor = t.post;
    else if (req.method === 'PUT') badgeColor = t.put;
    else if (req.method === 'PATCH') badgeColor = t.patch;
    else if (req.method === 'DELETE') badgeColor = t.delete;

    const interpolatedUrl = Interpolator.interpolate(req.url, this.collection);

    // URL Bar
    this.requestPane.setContent(
      ` {bold}{${badgeColor}-fg}${req.method}{/}  {bold}${interpolatedUrl}{/}`
    );

    // Tabs Bar
    const tabs = [
      { id: 'headers', label: `Headers (${req.headers.length})` },
      { id: 'params', label: `Params (${req.params.length})` },
      { id: 'body', label: `Body (${req.bodyType})` },
      { id: 'auth', label: `Auth (${req.authType})` },
      { id: 'pre-script', label: `Pre-Script${req.preRequestScript ? ' ●' : ''}` },
      { id: 'post-script', label: `Post-Script${req.postResponseScript ? ' ●' : ''}` },
      { id: 'docs', label: `Docs` }
    ];

    const tabStr = tabs.map(tab => {
      if (tab.id === this.activeRequestTab) {
        return `{bold}{${t.accent}-fg}[ ${tab.label} ]{/}`;
      }
      return `{${t.subtext}-fg}  ${tab.label}  {/}`;
    }).join(' ');

    this.requestTabsBox.setContent(tabStr);

    // Content
    let content = '';
    if (this.activeRequestTab === 'headers') {
      if (req.headers.length === 0) content = '{#6c7086-fg}No custom headers.{/}';
      else {
        content = req.headers.map(h => {
          const val = Interpolator.interpolate(h.value, this.collection);
          return `{bold}{#89b4fa-fg}${h.name}{/}: {#cdd6f4-fg}${val}{/}`;
        }).join('\n');
      }
    } else if (this.activeRequestTab === 'params') {
      if (req.params.length === 0) content = '{#6c7086-fg}No query parameters.{/}';
      else {
        content = req.params.map(p => {
          const val = Interpolator.interpolate(p.value, this.collection);
          return `{bold}{#94e2d5-fg}${p.name}{/} = {#cdd6f4-fg}${val}{/}`;
        }).join('\n');
      }
    } else if (this.activeRequestTab === 'body') {
      if (req.bodyType === 'none' || !req.bodyContent) {
        content = '{#6c7086-fg}No body configured.{/}';
      } else {
        content = Interpolator.interpolate(req.bodyContent, this.collection);
      }
    } else if (this.activeRequestTab === 'auth') {
      content = `{bold}Auth Type:{/} ${req.authType}\n`;
      if (req.authType === 'bearer') {
        const token = req.authBearerToken || this.collection.collectionAuthBearerToken;
        const resolved = token ? Interpolator.interpolate(token, this.collection) : '(none)';
        content += `{bold}Bearer Token:{/} ${resolved}\n`;
      } else if (req.authType === 'oauth2' && req.authOAuth2) {
        content += `{bold}Grant Type:{/} ${req.authOAuth2.grantType}\n`;
        content += `{bold}Token URL:{/} ${req.authOAuth2.tokenUrl}\n`;
        content += `{bold}Client ID:{/} ${req.authOAuth2.clientId}\n`;
      }
    } else if (this.activeRequestTab === 'pre-script') {
      content = req.preRequestScript || '{#6c7086-fg}No pre-request script.{/}';
    } else if (this.activeRequestTab === 'post-script') {
      content = req.postResponseScript || '{#6c7086-fg}No post-response script.{/}';
    } else if (this.activeRequestTab === 'docs') {
      content = req.docs || '{#6c7086-fg}No documentation available.{/}';
    }

    this.requestContentBox.setContent(content);
    this.screen.render();
  }

  private updateResponseView() {
    const t = this.getTheme();

    if (!this.lastResponse) {
      this.responseHeaderBox.setContent(`{${t.subtext}-fg} No response yet. Press {bold}[Enter]{/} or {bold}[Space]{/} to send.{/}`);
      this.responseTabsBox.setContent('');
      this.responseContentBox.setContent('');
      this.screen.render();
      return;
    }

    const resp = this.lastResponse;
    const statusColor = resp.status >= 200 && resp.status < 300
      ? t.statusSuccess
      : resp.status >= 400
        ? t.statusError
        : t.statusWarn;

    const sizeFormatted = resp.sizeBytes > 1024
      ? `${(resp.sizeBytes / 1024).toFixed(1)} KB`
      : `${resp.sizeBytes} B`;

    this.responseHeaderBox.setContent(
      ` {bold}{${statusColor}-fg}${resp.status} ${resp.statusText}{/}  ` +
      `{${t.subtext}-fg}Time:{/} {bold}${resp.durationMs} ms{/}  ` +
      `{${t.subtext}-fg}Size:{/} {bold}${sizeFormatted}{/}`
    );

    // Tabs
    const tabs = [
      { id: 'body', label: 'Response Body' },
      { id: 'headers', label: `Headers (${Object.keys(resp.headers).length})` },
      { id: 'console', label: `Console (${this.scriptRunner.logs.length})` }
    ];

    const tabStr = tabs.map(tab => {
      if (tab.id === this.activeResponseTab) {
        return `{bold}{${t.accent}-fg}[ ${tab.label} ]{/}`;
      }
      return `{${t.subtext}-fg}  ${tab.label}  {/}`;
    }).join(' ');

    this.responseTabsBox.setContent(tabStr);

    // Content
    let content = '';
    if (this.activeResponseTab === 'body') {
      if (resp.jsonBody) {
        content = this.highlightJson(resp.jsonBody);
      } else {
        content = resp.body;
      }
    } else if (this.activeResponseTab === 'headers') {
      content = Object.entries(resp.headers)
        .map(([k, v]) => `{bold}{#89b4fa-fg}${k}{/}: {#cdd6f4-fg}${v}{/}`)
        .join('\n');
    } else if (this.activeResponseTab === 'console') {
      if (this.scriptRunner.logs.length === 0) {
        content = '{#6c7086-fg}No console logs recorded.{/}';
      } else {
        content = this.scriptRunner.logs.map(l => {
          let color = '#cdd6f4';
          if (l.type === 'error') color = t.statusError;
          else if (l.type === 'warn') color = t.statusWarn;
          else if (l.type === 'info') color = t.statusInfo;
          return `[{#6c7086-fg}${l.timestamp.toTimeString().split(' ')[0]}{/}] {${color}-fg}${l.message}{/}`;
        }).join('\n');
      }
    }

    this.responseContentBox.setContent(content);
    this.screen.render();
  }

  private highlightJson(obj: any): string {
    const t = this.getTheme();
    const str = JSON.stringify(obj, null, 2);
    // Lightweight ANSI syntax highlighting
    return str
      .replace(/"(.*?)":/g, `{bold}{${t.put}-fg}"$1"{/}:`)
      .replace(/: "(.*?)"/g, `: {${t.get}-fg}"$1"{/}`)
      .replace(/: (\d+)/g, `: {${t.post}-fg}$1{/}`)
      .replace(/: (true|false)/g, `: {${t.accent}-fg}$1{/}`)
      .replace(/: (null)/g, `: {${t.delete}-fg}$1{/}`);
  }

  private updateStatusBar(msg: string) {
    const t = this.getTheme();
    this.statusBar.setContent(
      ` {bold}[Enter/Space]{/} Send  ` +
      `{bold}[O]{/} OAuth2  ` +
      `{bold}[E]{/} Env  ` +
      `{bold}[T]{/} Theme  ` +
      `{bold}[1-7]{/} Req Tabs  ` +
      `{bold}[Tab]{/} Focus  ` +
      `{bold}[q]{/} Quit   ` +
      `{right}{bold}{${t.accent}-fg}${msg}{/} `
    );
    this.screen.render();
  }

  private bindEvents() {
    // 1. Sidebar selection change
    this.sidebarList.on('select item', (_item: any, index: number) => {
      this.selectedRequestIndex = index;
      this.updateRequestView();
    });

    // 2. Mouse click on sidebar item
    this.sidebarList.on('click', () => {
      this.selectedRequestIndex = (this.sidebarList as any).selected;
      this.updateRequestView();
    });

    // 3. Mouse click on Request Tabs
    this.requestTabsBox.on('click', (data: any) => {
      const clickX = data.x;
      // Cycle tabs on click
      const tabs: Array<typeof this.activeRequestTab> = ['headers', 'params', 'body', 'auth', 'pre-script', 'post-script', 'docs'];
      const curIdx = tabs.indexOf(this.activeRequestTab);
      this.activeRequestTab = tabs[(curIdx + 1) % tabs.length];
      this.updateRequestView();
    });

    // 4. Mouse click on Response Tabs
    this.responseTabsBox.on('click', () => {
      const tabs: Array<typeof this.activeResponseTab> = ['body', 'headers', 'console'];
      const curIdx = tabs.indexOf(this.activeResponseTab);
      this.activeResponseTab = tabs[(curIdx + 1) % tabs.length];
      this.updateResponseView();
    });

    // 5. Global Hotkeys
    this.screen.key(['q', 'C-c'], () => {
      return process.exit(0);
    });

    // Send Request
    this.screen.key(['enter', 'space'], async () => {
      await this.sendActiveRequest();
    });

    // OAuth2 Trigger
    this.screen.key(['o', 'O'], async () => {
      await this.triggerOAuth2();
    });

    // Cycle Theme
    this.screen.key(['t', 'T'], () => {
      const newTheme = this.themeManager.cycle();
      this.applyTheme();
      this.populateSidebar();
      this.updateRequestView();
      this.updateResponseView();
      this.updateStatusBar(`Theme: ${newTheme.name}`);
    });

    // Cycle Environment
    this.screen.key(['e', 'E'], () => {
      this.cycleEnvironment();
    });

    // Number keys for request tabs
    this.screen.key(['1'], () => { this.activeRequestTab = 'headers'; this.updateRequestView(); });
    this.screen.key(['2'], () => { this.activeRequestTab = 'params'; this.updateRequestView(); });
    this.screen.key(['3'], () => { this.activeRequestTab = 'body'; this.updateRequestView(); });
    this.screen.key(['4'], () => { this.activeRequestTab = 'auth'; this.updateRequestView(); });
    this.screen.key(['5'], () => { this.activeRequestTab = 'pre-script'; this.updateRequestView(); });
    this.screen.key(['6'], () => { this.activeRequestTab = 'post-script'; this.updateRequestView(); });
    this.screen.key(['7'], () => { this.activeRequestTab = 'docs'; this.updateRequestView(); });

    // Tab key switches focus
    this.screen.key(['tab'], () => {
      if (this.screen.focused === this.sidebarList) {
        this.responseContentBox.focus();
      } else if (this.screen.focused === this.responseContentBox) {
        this.requestContentBox.focus();
      } else {
        this.sidebarList.focus();
      }
      this.screen.render();
    });
  }

  private cycleEnvironment() {
    if (this.collection.environments.length === 0) {
      this.updateStatusBar('No environments configured');
      return;
    }
    const envs = this.collection.environments.map(e => e.name);
    const curIdx = envs.indexOf(this.collection.activeEnvironment || '');
    const nextIdx = (curIdx + 1) % envs.length;
    this.collection.activeEnvironment = envs[nextIdx];
    this.updateHeader();
    this.updateRequestView();
    this.updateStatusBar(`Environment switched to: [${envs[nextIdx]}]`);
  }

  private async sendActiveRequest() {
    if (this.isRunning) return;
    const req = this.allRequests[this.selectedRequestIndex];
    if (!req) return;

    this.isRunning = true;
    this.scriptRunner.clearLogs();
    this.updateStatusBar(`Executing ${req.method} ${req.name}...`);

    try {
      // 1. Run Pre-request scripts (including collection script with bru.runRequest if needed!)
      await this.scriptRunner.runPreRequestScripts(req);
      this.updateHeader(); // in case script updated access_token

      // 2. Execute HTTP call
      const resp = await HttpClient.execute(req, this.collection);
      this.lastResponse = resp;

      // 3. Run Post-response script
      await this.scriptRunner.runPostResponseScript(req, resp);
      this.updateHeader(); // in case script saved access_token

      this.updateResponseView();
      this.updateStatusBar(`Done: ${resp.status} ${resp.statusText} (${resp.durationMs}ms)`);
    } catch (err: any) {
      this.updateStatusBar(`Error: ${err.message}`);
    } finally {
      this.isRunning = false;
    }
  }

  private async triggerOAuth2() {
    const req = this.allRequests[this.selectedRequestIndex];
    let oauthConfig = req?.authOAuth2;

    // If request doesn't have it, check if collection has token endpoint in environment
    if (!oauthConfig && this.collection.activeEnvironment) {
      const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
      if (env) {
        const tokenUrl = env.variables['APIGEE_HOST'] ? `${env.variables['APIGEE_HOST']}/auth/oauth/v2/token` : undefined;
        const clientId = env.variables['KC_CLIENT_ID'];
        const clientSecret = env.variables['KC_CLIENT_SECRET'];
        if (tokenUrl && clientId) {
          oauthConfig = {
            grantType: 'authorization_code',
            tokenUrl,
            clientId,
            clientSecret,
            authUrl: env.variables['APIGEE_HOST'] ? `${env.variables['APIGEE_HOST']}/auth/oauth/v2/authorize` : undefined
          };
        }
      }
    }

    if (!oauthConfig) {
      this.updateStatusBar('No OAuth2 config found for request or environment');
      return;
    }

    this.updateStatusBar('Starting OAuth2 browser authentication...');
    try {
      const result = await OAuth2Handler.authenticate(oauthConfig, this.collection, (msg) => {
        this.updateStatusBar(msg);
      });
      this.updateHeader();
      this.updateRequestView();
      this.updateStatusBar(`OAuth2 success! Token saved: ${result.accessToken.slice(0, 15)}...`);
    } catch (err: any) {
      this.updateStatusBar(`OAuth2 error: ${err.message}`);
    }
  }
}
