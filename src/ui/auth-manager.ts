import blessed from 'neo-blessed';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { bruToJsonV2, jsonToBruV2 } from '@usebruno/lang';
import { BrunoCollection, BrunoRequest, OAuth2Config, ThemeColors } from '../types';
import { ThemeManager } from '../themes';
import { Interpolator } from '../core/interpolation';
import { OAuth2Handler } from '../core/oauth2';

export class AuthManagerModal {
  private screen: blessed.Widgets.Screen;
  private collection: BrunoCollection;
  private request: BrunoRequest;
  private themeManager: ThemeManager;
  private onUpdate: () => void;
  private onClose?: () => void;

  private modal?: blessed.Widgets.BoxElement;
  private modesList!: blessed.Widgets.ListElement;
  private fieldsList!: blessed.Widgets.ListElement;
  private statusBox!: blessed.Widgets.BoxElement;
  private footerBar!: blessed.Widgets.BoxElement;
  private closeBtn!: blessed.Widgets.BoxElement;

  private activeFocusPane: 'modes' | 'fields' = 'modes';
  private selectedModeIndex: number = 0;
  private selectedFieldIndex: number = 0;
  private isPromptOpen: boolean = false;
  private isAuthenticating: boolean = false;

  private keypressHandler?: (ch: any, key: any) => void;

  private readonly authModes: Array<{ id: BrunoRequest['authType']; label: string }> = [
    { id: 'inherit', label: 'Inherit from Collection' },
    { id: 'none', label: 'No Authentication' },
    { id: 'bearer', label: 'Bearer Token' },
    { id: 'basic', label: 'Basic Auth' },
    { id: 'oauth2', label: 'OAuth 2.0' }
  ];

  constructor(
    screen: blessed.Widgets.Screen,
    collection: BrunoCollection,
    request: BrunoRequest,
    themeManager: ThemeManager,
    onUpdate: () => void,
    onClose?: () => void
  ) {
    this.screen = screen;
    this.collection = collection;
    this.request = request;
    this.themeManager = themeManager;
    this.onUpdate = onUpdate;
    this.onClose = onClose;
  }

  private getTheme(): ThemeColors {
    return this.themeManager.getCurrent();
  }

  public isOpen(): boolean {
    return !!this.modal;
  }

  public show() {
    if (this.modal) return;
    const t = this.getTheme();

    const curModeIdx = this.authModes.findIndex(m => m.id === this.request.authType);
    this.selectedModeIndex = curModeIdx !== -1 ? curModeIdx : 0;
    this.activeFocusPane = 'modes';
    this.isPromptOpen = false;

    // Ensure request has oauth2 object initialized if empty
    if (!this.request.authOAuth2) {
      this.initDefaultOAuth2Config();
    }

    this.modal = blessed.box({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '88%',
      height: '80%',
      border: { type: 'line' },
      style: {
        bg: t.bg,
        fg: t.fg,
        border: { fg: t.accent }
      },
      tags: true,
      mouse: true
    });

    // Header Title
    blessed.box({
      parent: this.modal,
      top: 0,
      left: 1,
      right: 12,
      height: 2,
      content: ` {bold}{${t.accent}-fg}🔐 AUTHENTICATION MANAGER{/}  ` +
               `{${t.subtext}-fg}Request:{/} {bold}{${t.put}-fg}[${this.request.method} ${this.request.name}]{/}  ` +
               `{${t.subtext}-fg}Mode:{/} {bold}{${t.options}-fg}[${this.request.authType.toUpperCase()}]{/} `,
      tags: true
    });

    // Close Button [ ✕ Close ]
    this.closeBtn = blessed.box({
      parent: this.modal,
      top: 0,
      right: 1,
      width: 11,
      height: 1,
      content: `{bold}{${t.delete}-fg}[ ✕ Close ]{/}`,
      tags: true,
      mouse: true,
      style: {
        bg: t.surface,
        hover: { bg: t.surfaceHover }
      }
    });

    // Left Pane: Auth Modes List
    this.modesList = blessed.list({
      parent: this.modal,
      top: 2,
      left: 1,
      width: '28%',
      bottom: 4,
      border: { type: 'line' },
      label: ` {bold}{${t.accent}-fg}Auth Mode{/} `,
      keys: true,
      vi: true,
      mouse: true,
      tags: true,
      style: {
        bg: t.surface,
        fg: t.fg,
        border: { fg: t.borderFocus },
        selected: { bg: t.selection, fg: t.selectionFg, bold: true },
        item: { bg: t.surface, fg: t.fg }
      }
    });

    // Right Pane: Details & Configuration Fields
    this.fieldsList = blessed.list({
      parent: this.modal,
      top: 2,
      left: '29%',
      right: 1,
      bottom: 4,
      border: { type: 'line' },
      label: ` {bold}{${t.put}-fg}Configuration & Fields (Enter / E to Edit, O to Get Token){/} `,
      keys: true,
      vi: true,
      mouse: true,
      tags: true,
      scrollable: true,
      style: {
        bg: t.surface,
        fg: t.fg,
        border: { fg: t.border },
        selected: { bg: t.selection, fg: t.selectionFg, bold: true },
        item: { bg: t.surface, fg: t.fg }
      },
      scrollbar: {
        ch: ' ',
        track: { bg: t.surface },
        style: { bg: t.selection }
      }
    });

    // Status / Message Bar
    this.statusBox = blessed.box({
      parent: this.modal,
      bottom: 2,
      left: 1,
      right: 1,
      height: 1,
      tags: true,
      style: { bg: t.surface, fg: t.fg }
    });

    // Bottom Action / Help Bar
    this.footerBar = blessed.box({
      parent: this.modal,
      bottom: 0,
      left: 1,
      right: 1,
      height: 2,
      tags: true,
      mouse: true,
      style: { bg: t.bg, fg: t.fg }
    });

    this.renderModesList();
    this.renderFieldsList();
    this.renderFooter();
    this.updateStatus();
    this.bindEvents();

    (this.modesList as any).select(this.selectedModeIndex);
    this.modesList.focus();
    this.screen.render();
  }

  private initDefaultOAuth2Config() {
    // Try to auto-derive from environment variables
    const getVar = (k: string) => {
      if (this.collection.activeEnvironment) {
        const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
        if (env && env.variables[k]) return env.variables[k];
      }
      return this.collection.runtimeVariables[k] || '';
    };

    const apigeeHost = getVar('APIGEE_HOST');
    const kcClientId = getVar('KC_CLIENT_ID');
    const kcClientSecret = getVar('KC_CLIENT_SECRET');
    const kcUser = getVar('KC_USERNAME');
    const kcPass = getVar('KC_PASSWORD');

    this.request.authOAuth2 = {
      grantType: kcUser ? 'password' : 'authorization_code',
      tokenUrl: apigeeHost ? '{{APIGEE_HOST}}/auth/oauth/v2/token' : '',
      authUrl: apigeeHost ? '{{APIGEE_HOST}}/auth/oauth/v2/authorize' : '',
      redirectUri: 'http://127.0.0.1:3000/callback',
      clientId: kcClientId ? '{{KC_CLIENT_ID}}' : '',
      clientSecret: kcClientSecret ? '{{KC_CLIENT_SECRET}}' : '',
      username: kcUser ? '{{KC_USERNAME}}' : '',
      password: kcPass ? '{{KC_PASSWORD}}' : '',
      scope: 'openid',
      pkce: true
    };
  }

  private renderModesList() {
    const t = this.getTheme();
    const items = this.authModes.map(m => {
      const isCur = m.id === this.request.authType;
      const bullet = isCur ? `{${t.get}-fg}● (active){/} ` : `{${t.subtext}-fg}○{/} `;
      return `${bullet}{bold}${m.label}{/}`;
    });
    this.modesList.setItems(items);
  }

  private getActiveTokenInfo(): { token?: string; isExpired: boolean; expiresInSec?: number } {
    let token: string | undefined;
    let exp: number = 0;

    if (this.collection.activeEnvironment) {
      const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
      if (env) {
        token = env.variables['access_token'];
        exp = Number(env.variables['access_token_exp']) || 0;
      }
    }
    if (!token) {
      token = this.collection.runtimeVariables['access_token'];
      exp = Number(this.collection.runtimeVariables['access_token_exp']) || 0;
    }

    if (!token) {
      return { isExpired: false };
    }

    const now = Date.now();
    const isExpired = exp > 0 && now > exp;
    const expiresInSec = exp > 0 ? Math.max(0, Math.round((exp - now) / 1000)) : undefined;

    return { token, isExpired, expiresInSec };
  }

  private renderFieldsList() {
    const t = this.getTheme();
    const mode = this.request.authType;
    const items: string[] = [];
    const tokenInfo = this.getActiveTokenInfo();

    if (mode === 'inherit') {
      const collMode = this.collection.collectionAuthType || 'none';
      items.push(`{bold}{${t.accent}-fg}Collection Auth Mode:{/} {${t.put}-fg}${collMode.toUpperCase()}{/}`);

      if (collMode === 'bearer') {
        const rawTok = this.collection.collectionAuthBearerToken || '{{access_token}}';
        const resTok = Interpolator.interpolate(rawTok, this.collection);
        items.push(`{bold}{${t.accent}-fg}Inherited Bearer Token:{/} {${t.fg}}${rawTok}{/}`);
        items.push(`  {${t.subtext}-fg}Resolved Preview:{/} ${resTok ? resTok.slice(0, 45) + '...' : '{#6c7086-fg}(empty){/}'}`);
      } else if (collMode === 'oauth2') {
        items.push(`{bold}{${t.accent}-fg}Inherited OAuth2:{/} Uses collection OAuth2 configuration.`);
      }

      items.push('');
      if (tokenInfo.token) {
        const expStr = tokenInfo.isExpired
          ? `{${t.statusError}-fg}▲ EXPIRED{/}`
          : `{${t.statusSuccess}-fg}● VALID (Expires in ${tokenInfo.expiresInSec}s){/}`;
        items.push(`{bold}Access Token Status:{/} ${expStr}`);
        items.push(`  {${t.subtext}-fg}Token:{/} ${tokenInfo.token.slice(0, 45)}...`);
      } else {
        items.push(`{bold}Access Token Status:{/} {${t.subtext}-fg}○ No token in environment. Press [O] or click below to authenticate.{/}`);
      }

      items.push('');
      items.push(`{bold}{${t.get}-fg}[ ⚡ Click here or Press [O] to Authenticate / Renew Token ]{/}`);
    } else if (mode === 'none') {
      items.push(`{${t.subtext}-fg}No authentication will be sent with this request.{/}`);
      items.push('');
      items.push(`{${t.subtext}-fg}Select another mode on the left or press [S] to save to .bru.{/}`);
    } else if (mode === 'bearer') {
      const rawToken = this.request.authBearerToken || '{{access_token}}';
      const resolved = Interpolator.interpolate(rawToken, this.collection);

      items.push(`{bold}{${t.accent}-fg}1. Token Template:{/} {${t.fg}}${rawToken}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
      items.push(`   {${t.subtext}-fg}Resolved Preview:{/} ${resolved ? resolved.slice(0, 50) + '...' : '{#6c7086-fg}(empty){/}'}`);
      items.push('');
      if (tokenInfo.token) {
        const expStr = tokenInfo.isExpired
          ? `{${t.statusError}-fg}▲ Token in Environment is EXPIRED{/}`
          : `{${t.statusSuccess}-fg}● Active Environment Token is VALID (${tokenInfo.expiresInSec}s){/}`;
        items.push(`{bold}Status:{/} ${expStr}`);
      }
      items.push('');
      items.push(`{bold}{${t.options}-fg}[ 💾 Click here or Press [S] to Save Bearer Auth to .bru ]{/}`);
    } else if (mode === 'basic') {
      const user = this.request.authBasicUser || '';
      const pass = this.request.authBasicPassword || '';
      items.push(`{bold}{${t.accent}-fg}1. Username:{/} {${t.fg}}${user || '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
      items.push(`{bold}{${t.accent}-fg}2. Password:{/} {${t.fg}}${pass ? '••••••••' : '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
      items.push('');
      items.push(`{bold}{${t.options}-fg}[ 💾 Click here or Press [S] to Save Basic Auth to .bru ]{/}`);
    } else if (mode === 'oauth2') {
      const o = this.request.authOAuth2!;
      const grant = o.grantType;

      items.push(`{bold}{${t.accent}-fg}1. Grant Type:{/} {bold}{${t.put}-fg}[ ${grant.toUpperCase()} ]{/}  {${t.subtext}-fg}(Enter/Click to toggle){/}`);
      items.push(`{bold}{${t.accent}-fg}2. Token URL (Access Token URL):{/} {${t.fg}}${o.tokenUrl || '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
      items.push(`{bold}{${t.accent}-fg}3. Client ID:{/} {${t.fg}}${o.clientId || '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
      items.push(`{bold}{${t.accent}-fg}4. Client Secret:{/} {${t.fg}}${o.clientSecret ? '••••••••' : '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);

      if (grant === 'password') {
        items.push(`{bold}{${t.accent}-fg}5. Username:{/} {${t.fg}}${o.username || '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
        items.push(`{bold}{${t.accent}-fg}6. Password:{/} {${t.fg}}${o.password ? '••••••••' : '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
      } else if (grant === 'authorization_code') {
        items.push(`{bold}{${t.accent}-fg}5. Authorization URL:{/} {${t.fg}}${o.authUrl || '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
        items.push(`{bold}{${t.accent}-fg}6. Callback URL:{/} {${t.fg}}${o.redirectUri || '{#6c7086-fg}(empty){/}'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
        items.push(`{bold}{${t.accent}-fg}7. PKCE:{/} {${t.fg}}${o.pkce !== false ? '{#a6e3a1-fg}[✔ Enabled]{/}' : '{#f38ba8-fg}[✘ Disabled]{/}'}{/}  {${t.put}-fg}[ Toggle ]{/}`);
      }

      items.push(`{bold}{${t.accent}-fg}Scope:{/} {${t.fg}}${o.scope || 'openid'}{/}  {${t.put}-fg}[ ✏ Edit ]{/}`);
      items.push('');

      if (tokenInfo.token) {
        const expStr = tokenInfo.isExpired
          ? `{${t.statusError}-fg}▲ Token EXPIRED{/}`
          : `{${t.statusSuccess}-fg}● Token VALID (Expires in ${tokenInfo.expiresInSec}s){/}`;
        items.push(`{bold}Current Token:{/} ${expStr}`);
        items.push(`  {${t.subtext}-fg}Value:{/} ${tokenInfo.token.slice(0, 45)}...`);
      } else {
        items.push(`{bold}Current Token:{/} {${t.subtext}-fg}○ No token found.{/}`);
      }

      items.push('');
      items.push(`{bold}{${t.get}-fg}[ ⚡ GET ACCESS TOKEN NOW (Press O or Click) ]{/}`);
      items.push(`{bold}{${t.options}-fg}[ 💾 SAVE TO .BRU FILE (Press S or Click) ]{/}`);
    }

    this.fieldsList.setItems(items);
  }

  private renderFooter() {
    const t = this.getTheme();
    this.footerBar.setContent(
      ` {bold}{${t.get}-fg}[O: Get Token]{/}  ` +
      `{bold}{${t.accent}-fg}[Enter/E: Edit/Toggle]{/}  ` +
      `{bold}{${t.options}-fg}[S: Save to .bru]{/}  ` +
      `{bold}[Tab: Switch Panel]{/}  ` +
      `{bold}[Esc/q: Close]{/}`
    );
  }

  private setStatus(msg: string) {
    const t = this.getTheme();
    this.statusBox.setContent(` {bold}{${t.accent}-fg}Status:{/} ${msg}`);
    this.screen.render();
  }

  private updateStatus() {
    const info = this.getActiveTokenInfo();
    const t = this.getTheme();
    if (info.token) {
      if (info.isExpired) {
        this.setStatus(`{${t.statusError}-fg}▲ Token in environment is EXPIRED. Press [O] to renew.{/}`);
      } else {
        this.setStatus(`{${t.statusSuccess}-fg}✔ Active token ready (expires in ${info.expiresInSec}s). Ready to send requests.{/}`);
      }
    } else {
      this.setStatus(`{${t.subtext}-fg}Ready. Select mode and configure authentication.{/}`);
    }
  }

  private updateBorders() {
    const t = this.getTheme();
    if (this.activeFocusPane === 'modes') {
      this.modesList.style.border.fg = t.borderFocus;
      this.fieldsList.style.border.fg = t.border;
    } else {
      this.modesList.style.border.fg = t.border;
      this.fieldsList.style.border.fg = t.borderFocus;
    }
    this.screen.render();
  }

  private switchAuthMode(modeId: BrunoRequest['authType']) {
    this.request.authType = modeId;
    if (modeId === 'oauth2' && !this.request.authOAuth2) {
      this.initDefaultOAuth2Config();
    }
    this.renderModesList();
    this.renderFieldsList();
    this.setStatus(`Auth mode switched to: [${modeId.toUpperCase()}]. Press [S] to persist.`);
    this.onUpdate();
  }

  private bindEvents() {
    const closeModal = () => {
      if (this.modal) {
        if (this.keypressHandler) {
          this.screen.removeListener('keypress', this.keypressHandler);
          this.keypressHandler = undefined;
        }
        this.modal.destroy();
        this.modal = undefined;
        this.isPromptOpen = false;
        this.onUpdate();
        if (this.onClose) {
          this.onClose();
        }
        this.screen.render();
      }
    };

    // Close button click [ ✕ Close ]
    this.closeBtn.on('click', () => {
      closeModal();
    });

    // 1. Modes list selection
    this.modesList.on('select item', (_item: any, index: number) => {
      this.selectedModeIndex = index;
    });

    this.modesList.on('select', (_item: any, index: number) => {
      const target = this.authModes[index];
      if (target) {
        this.switchAuthMode(target.id);
      }
    });

    this.modesList.on('click', () => {
      this.activeFocusPane = 'modes';
      const idx = (this.modesList as any).selected;
      if (typeof idx === 'number' && this.authModes[idx]) {
        this.selectedModeIndex = idx;
        this.switchAuthMode(this.authModes[idx].id);
      }
      this.updateBorders();
    });

    // 2. Fields list selection
    this.fieldsList.on('click', () => {
      this.activeFocusPane = 'fields';
      this.updateBorders();
    });

    this.fieldsList.on('select item', (_item: any, index: number) => {
      this.selectedFieldIndex = index;
    });

    this.fieldsList.on('select', (_item: any, index: number) => {
      this.handleFieldAction(index);
    });

    // 3. Global Keypress Handler
    this.keypressHandler = (ch: any, key: any) => {
      if (!this.modal) return;
      if (this.isPromptOpen) return;
      if (!key) return;

      // ESCAPE or 'q' -> Closes modal
      if (key.name === 'escape' || ch === 'q' || ch === 'Q') {
        closeModal();
        return;
      }

      // TAB -> Toggles focus between modes and fields
      if (key.name === 'tab') {
        if (this.activeFocusPane === 'modes') {
          this.activeFocusPane = 'fields';
          this.fieldsList.focus();
        } else {
          this.activeFocusPane = 'modes';
          this.modesList.focus();
        }
        this.updateBorders();
        return;
      }

      // Left / Right arrows
      if (key.name === 'left') {
        this.activeFocusPane = 'modes';
        this.modesList.focus();
        this.updateBorders();
        return;
      }
      if (key.name === 'right') {
        this.activeFocusPane = 'fields';
        this.fieldsList.focus();
        this.updateBorders();
        return;
      }

      // 'O' -> Trigger OAuth2 / Authenticate
      if (ch === 'o' || ch === 'O') {
        this.executeTokenAcquisition();
        return;
      }

      // 'S' -> Save to .bru
      if (ch === 's' || ch === 'S') {
        this.saveCurrentAuth();
        return;
      }

      // 'E' -> Edit selected field
      if (ch === 'e' || ch === 'E') {
        if (this.activeFocusPane === 'fields') {
          const idx = (this.fieldsList as any).selected;
          this.handleFieldAction(idx);
        }
        return;
      }
    };

    this.screen.on('keypress', this.keypressHandler);
  }

  private handleFieldAction(index: number) {
    const mode = this.request.authType;

    if (mode === 'inherit') {
      this.executeTokenAcquisition();
      return;
    }

    if (mode === 'bearer') {
      if (index === 0) {
        this.promptEdit('Bearer Token (e.g. {{access_token}}):', this.request.authBearerToken || '{{access_token}}', val => {
          this.request.authBearerToken = val;
          this.renderFieldsList();
          this.onUpdate();
        });
      } else {
        this.saveCurrentAuth();
      }
      return;
    }

    if (mode === 'basic') {
      if (index === 0) {
        this.promptEdit('Basic Auth Username:', this.request.authBasicUser || '', val => {
          this.request.authBasicUser = val;
          this.renderFieldsList();
          this.onUpdate();
        });
      } else if (index === 1) {
        this.promptEdit('Basic Auth Password:', this.request.authBasicPassword || '', val => {
          this.request.authBasicPassword = val;
          this.renderFieldsList();
          this.onUpdate();
        });
      } else {
        this.saveCurrentAuth();
      }
      return;
    }

    if (mode === 'oauth2') {
      const o = this.request.authOAuth2!;
      const grant = o.grantType;

      if (index === 0) {
        // Toggle grant type: authorization_code -> client_credentials -> password -> authorization_code
        if (grant === 'authorization_code') o.grantType = 'client_credentials';
        else if (grant === 'client_credentials') o.grantType = 'password';
        else o.grantType = 'authorization_code';
        this.renderFieldsList();
        this.setStatus(`Grant type changed to: [${o.grantType.toUpperCase()}]`);
        this.onUpdate();
        return;
      }

      if (index === 1) {
        this.promptEdit('Token URL (e.g. {{APIGEE_HOST}}/auth/oauth/v2/token):', o.tokenUrl || '', val => {
          o.tokenUrl = val;
          this.renderFieldsList();
          this.onUpdate();
        });
        return;
      }

      if (index === 2) {
        this.promptEdit('Client ID (e.g. {{KC_CLIENT_ID}}):', o.clientId || '', val => {
          o.clientId = val;
          this.renderFieldsList();
          this.onUpdate();
        });
        return;
      }

      if (index === 3) {
        this.promptEdit('Client Secret (e.g. {{KC_CLIENT_SECRET}}):', o.clientSecret || '', val => {
          o.clientSecret = val;
          this.renderFieldsList();
          this.onUpdate();
        });
        return;
      }

      if (grant === 'password') {
        if (index === 4) {
          this.promptEdit('Username (e.g. {{KC_USERNAME}}):', o.username || '', val => {
            o.username = val;
            this.renderFieldsList();
            this.onUpdate();
          });
          return;
        }
        if (index === 5) {
          this.promptEdit('Password (e.g. {{KC_PASSWORD}}):', o.password || '', val => {
            o.password = val;
            this.renderFieldsList();
            this.onUpdate();
          });
          return;
        }
      } else if (grant === 'authorization_code') {
        if (index === 4) {
          this.promptEdit('Authorization URL (e.g. {{APIGEE_HOST}}/auth/oauth/v2/authorize):', o.authUrl || '', val => {
            o.authUrl = val;
            this.renderFieldsList();
            this.onUpdate();
          });
          return;
        }
        if (index === 5) {
          this.promptEdit('Callback URL (Redirect URI):', o.redirectUri || 'http://127.0.0.1:3000/callback', val => {
            o.redirectUri = val;
            this.renderFieldsList();
            this.onUpdate();
          });
          return;
        }
        if (index === 6) {
          o.pkce = o.pkce === false ? true : false;
          this.renderFieldsList();
          this.setStatus(`PKCE toggled: ${o.pkce ? 'ENABLED' : 'DISABLED'}`);
          this.onUpdate();
          return;
        }
      }

      // Check if clicked Get Token or Save button
      const itemsCount = (this.fieldsList as any).items.length;
      if (index >= itemsCount - 2) {
        if (index === itemsCount - 2) {
          this.executeTokenAcquisition();
        } else {
          this.saveCurrentAuth();
        }
      }
    }
  }

  private promptEdit(title: string, currentVal: string, onDone: (val: string) => void) {
    const t = this.getTheme();
    const promptBox = blessed.prompt({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '65%',
      height: 'shrink',
      border: { type: 'line' },
      tags: true,
      keys: true,
      mouse: true,
      style: {
        bg: t.surface,
        fg: t.fg,
        border: { fg: t.accent }
      }
    });

    this.isPromptOpen = true;
    promptBox.setFront();
    promptBox.input(title, currentVal || '', (err, value) => {
      this.isPromptOpen = false;
      promptBox.destroy();
      this.fieldsList.focus();
      if (!err && value !== null && value !== undefined) {
        onDone(value.trim());
      }
      this.screen.render();
    });
  }

  public async executeTokenAcquisition() {
    if (this.isAuthenticating) return;
    this.isAuthenticating = true;
    const t = this.getTheme();
    this.setStatus(`{bold}{${t.accent}-fg}⚡ Authenticating... Please wait.{/}`);

    try {
      let cfg: OAuth2Config | undefined = this.request.authOAuth2;

      // If mode is inherit or config is incomplete, look in environment or collection
      if (!cfg || !cfg.tokenUrl) {
        if (this.collection.collectionAuthOAuth2) {
          cfg = this.collection.collectionAuthOAuth2;
        } else if (this.collection.activeEnvironment) {
          const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
          if (env) {
            const host = env.variables['APIGEE_HOST'] || 'https://apitest.tim.it';
            cfg = {
              grantType: env.variables['KC_USERNAME'] ? 'password' : 'authorization_code',
              tokenUrl: `${host}/auth/oauth/v2/token`,
              authUrl: `${host}/auth/oauth/v2/authorize`,
              redirectUri: 'http://127.0.0.1:3000/callback',
              clientId: env.variables['KC_CLIENT_ID'] || '',
              clientSecret: env.variables['KC_CLIENT_SECRET'] || '',
              username: env.variables['KC_USERNAME'] || '',
              password: env.variables['KC_PASSWORD'] || '',
              scope: 'openid',
              pkce: true
            };
          }
        }
      }

      if (!cfg || !cfg.tokenUrl) {
        throw new Error('No OAuth2 Token URL configured. Enter Token URL or check Environment variables.');
      }

      const res = await OAuth2Handler.authenticate(cfg, this.collection, (msg) => {
        this.setStatus(`{${t.accent}-fg}${msg}{/}`);
      });

      this.setStatus(`{${t.statusSuccess}-fg}✔ OAuth2 Success! Access token received: ${res.accessToken.slice(0, 20)}... (Expires in ${res.expiresIn || 300}s){/}`);
      this.renderFieldsList();
      this.onUpdate();
    } catch (err: any) {
      this.setStatus(`{${t.statusError}-fg}✖ Authentication failed: ${err.message}{/}`);
    } finally {
      this.isAuthenticating = false;
    }
  }

  public saveCurrentAuth() {
    try {
      const filePath = this.request.filePath;
      if (!fs.existsSync(filePath)) {
        throw new Error(`Request file not found: ${filePath}`);
      }

      const raw = fs.readFileSync(filePath, 'utf8');
      const json = bruToJsonV2(raw) as any;

      if (!json.http) json.http = {};
      json.http.auth = this.request.authType;

      if (!json.auth) json.auth = {};
      json.auth.mode = this.request.authType;

      if (this.request.authType === 'bearer') {
        json.auth.bearer = {
          token: this.request.authBearerToken || '{{access_token}}'
        };
      } else if (this.request.authType === 'basic') {
        json.auth.basic = {
          username: this.request.authBasicUser || '',
          password: this.request.authBasicPassword || ''
        };
      } else if (this.request.authType === 'oauth2' && this.request.authOAuth2) {
        const o = this.request.authOAuth2;
        json.auth.oauth2 = {
          grantType: o.grantType,
          accessTokenUrl: o.tokenUrl,
          authorizationUrl: o.authUrl || '',
          callbackUrl: o.redirectUri || '',
          clientId: o.clientId,
          clientSecret: o.clientSecret || '',
          username: o.username || '',
          password: o.password || '',
          scope: o.scope || 'openid',
          pkce: o.pkce !== false
        };
      }

      const updated = jsonToBruV2(json);
      fs.writeFileSync(filePath, updated, 'utf8');
      this.setStatus(`✔ Successfully saved auth configuration to ${path.basename(filePath)}!`);
    } catch (err: any) {
      this.setStatus(`✖ Failed to save auth: ${err.message}`);
    }
  }
}
