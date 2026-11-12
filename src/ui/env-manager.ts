import blessed from 'neo-blessed';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { envJsonToBruV2 } from '@usebruno/lang';
import { BrunoCollection, BrunoEnvironment, ThemeColors } from '../types';
import { ThemeManager } from '../themes';

export class EnvironmentManagerModal {
  private screen: blessed.Widgets.Screen;
  private collection: BrunoCollection;
  private themeManager: ThemeManager;
  private onUpdate: () => void;
  private onClose?: () => void;

  private modal?: blessed.Widgets.BoxElement;
  private selectedEnvIndex: number = 0;
  private selectedVarIndex: number = 0;
  private activeFocusPane: 'envs' | 'vars' = 'envs';
  private isPromptOpen: boolean = false;

  // Widgets
  private envsList!: blessed.Widgets.ListElement;
  private varsList!: blessed.Widgets.ListElement;
  private statusBox!: blessed.Widgets.BoxElement;
  private footerBar!: blessed.Widgets.BoxElement;
  private closeBtn!: blessed.Widgets.BoxElement;

  private keypressHandler?: (ch: any, key: any) => void;

  constructor(
    screen: blessed.Widgets.Screen,
    collection: BrunoCollection,
    themeManager: ThemeManager,
    onUpdate: () => void,
    onClose?: () => void
  ) {
    this.screen = screen;
    this.collection = collection;
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

    if (this.collection.environments.length === 0) {
      this.collection.environments.push({
        name: 'Local',
        variables: {}
      });
      this.collection.activeEnvironment = 'Local';
    }

    const curActiveIdx = this.collection.environments.findIndex(
      e => e.name === this.collection.activeEnvironment
    );
    this.selectedEnvIndex = curActiveIdx !== -1 ? curActiveIdx : 0;
    this.activeFocusPane = 'envs';
    this.isPromptOpen = false;

    this.modal = blessed.box({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '85%',
      height: '75%',
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
      content: ` {bold}{${t.accent}-fg}🌍 ENVIRONMENT & VARIABLES MANAGER{/}  ` +
               `{${t.subtext}-fg}Active:{/} {bold}{${t.options}-fg}[${this.collection.activeEnvironment || 'None'}]{/} `,
      tags: true
    });

    // Top Right Close Button [ ✕ Close ]
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

    // Left Pane: Environments List (width 32%)
    this.envsList = blessed.list({
      parent: this.modal,
      top: 2,
      left: 1,
      width: '32%',
      bottom: 4,
      border: { type: 'line' },
      label: ` {bold}{${t.accent}-fg}Environments{/} `,
      keys: true,
      vi: true,
      mouse: true,
      tags: true,
      scrollable: true,
      style: {
        bg: t.surface,
        fg: t.fg,
        border: { fg: t.borderFocus },
        selected: { bg: t.selection, fg: t.selectionFg, bold: true },
        item: { bg: t.surface, fg: t.fg }
      },
      scrollbar: {
        ch: ' ',
        track: { bg: t.surface },
        style: { bg: t.selection }
      }
    });

    // Right Pane: Variables List (width 68%)
    this.varsList = blessed.list({
      parent: this.modal,
      top: 2,
      left: '33%',
      right: 1,
      bottom: 4,
      border: { type: 'line' },
      label: ` {bold}{${t.put}-fg}Variables (Enter / E to Edit){/} `,
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

    this.renderEnvsList();
    this.renderVarsList();
    this.renderFooter();
    this.bindEvents();

    (this.envsList as any).select(this.selectedEnvIndex);
    this.envsList.focus();
    this.screen.render();
  }

  private renderEnvsList() {
    const t = this.getTheme();
    const envs = this.collection.environments;

    const items = envs.map(e => {
      const isCur = e.name === this.collection.activeEnvironment;
      const bullet = isCur ? `{${t.get}-fg}● (active){/} ` : `{${t.subtext}-fg}○{/} `;
      const varCount = Object.keys(e.variables).length;
      return `${bullet}{bold}${e.name}{/} {${t.subtext}-fg}(${varCount}){/}`;
    });

    items.push(`{${t.put}-fg}[+ New Environment]{/}`);
    this.envsList.setItems(items);
  }

  private renderVarsList() {
    const t = this.getTheme();
    const env = this.collection.environments[this.selectedEnvIndex];
    if (!env) {
      this.varsList.setItems([`{${t.subtext}-fg}No environment selected{/}`]);
      return;
    }

    const entries = Object.entries(env.variables);
    if (entries.length === 0) {
      this.varsList.setItems([
        `{${t.subtext}-fg}No variables in [${env.name}]. Press [N] to add a variable.{/}`
      ]);
      return;
    }

    const items = entries.map(([key, val]) => {
      const isSecret = key.toLowerCase().includes('secret') || key.toLowerCase().includes('password');
      let displayVal = String(val ?? '');
      if (isSecret && displayVal.length > 0) {
        displayVal = '•••••••• (' + displayVal.slice(0, 3) + '...)';
      } else if (displayVal.length > 55) {
        displayVal = displayVal.slice(0, 52) + '...';
      }
      return `{bold}{${t.accent}-fg}${key}:{/} {${t.fg}}${displayVal || '{#6c7086-fg}(empty){/}'}{/}`;
    });

    this.varsList.setItems(items);
  }

  private renderFooter() {
    const t = this.getTheme();
    const env = this.collection.environments[this.selectedEnvIndex];
    const isCur = env?.name === this.collection.activeEnvironment;

    const activateBtn = isCur
      ? `{${t.subtext}-fg}[✔ Active]{/}`
      : `{bold}{${t.get}-fg}[A: Set Active]{/}`;

    this.footerBar.setContent(
      ` ${activateBtn}  ` +
      `{bold}{${t.accent}-fg}[Enter/E: Edit Value]{/}  ` +
      `{bold}{${t.put}-fg}[N: Add Var]{/}  ` +
      `{bold}{${t.delete}-fg}[D: Delete]{/}  ` +
      `{bold}{${t.options}-fg}[S: Save File]{/}  ` +
      `{bold}[Tab: Switch Panel]{/}  ` +
      `{bold}[Esc/q: Close]{/}`
    );
  }

  private setStatus(msg: string) {
    const t = this.getTheme();
    this.statusBox.setContent(` {bold}{${t.accent}-fg}Status:{/} ${msg}`);
    this.screen.render();
  }

  private updateBorders() {
    const t = this.getTheme();
    if (this.activeFocusPane === 'envs') {
      this.envsList.style.border.fg = t.borderFocus;
      this.varsList.style.border.fg = t.border;
    } else {
      this.envsList.style.border.fg = t.border;
      this.varsList.style.border.fg = t.borderFocus;
    }
    this.renderFooter();
    this.screen.render();
  }

  public activateSelectedEnv() {
    const env = this.collection.environments[this.selectedEnvIndex];
    if (env) {
      this.collection.activeEnvironment = env.name;
      this.renderEnvsList();
      this.renderFooter();
      this.setStatus(`✔ Active environment changed to: [${env.name}]`);
      this.onUpdate();
    }
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

    // 1. Envs List: Selection preview
    this.envsList.on('select item', (_item: any, index: number) => {
      if (index < this.collection.environments.length) {
        this.selectedEnvIndex = index;
        this.renderVarsList();
        this.renderFooter();
        this.screen.render();
      }
    });

    // 2. Envs List: Enter / Click
    this.envsList.on('select', (_item: any, index: number) => {
      const envs = this.collection.environments;
      if (index === envs.length) {
        this.promptAddEnvironment();
      } else {
        this.activateSelectedEnv();
      }
    });

    // Click on Envs List switches focus
    this.envsList.on('click', () => {
      this.activeFocusPane = 'envs';
      const idx = (this.envsList as any).selected;
      if (typeof idx === 'number' && idx < this.collection.environments.length) {
        this.selectedEnvIndex = idx;
        this.activateSelectedEnv();
      }
      this.updateBorders();
    });

    // Click on Vars List switches focus & allows editing
    this.varsList.on('click', () => {
      this.activeFocusPane = 'vars';
      const idx = (this.varsList as any).selected;
      if (typeof idx === 'number') {
        this.selectedVarIndex = idx;
      }
      this.updateBorders();
    });

    // 3. Vars List: Selection
    this.varsList.on('select item', (_item: any, index: number) => {
      this.selectedVarIndex = index;
    });

    // 4. Vars List: Enter -> Edit Variable Value!
    this.varsList.on('select', (_item: any, index: number) => {
      this.promptEditVariable(index);
    });

    // 5. Global Keypress Handler (Directly on screen so Escape and shortcuts ALWAYS work!)
    this.keypressHandler = (ch: any, key: any) => {
      if (!this.modal) return;
      if (this.isPromptOpen) return;
      if (!key) return;

      // ESCAPE or 'q' -> Always closes the modal!
      if (key.name === 'escape' || ch === 'q' || ch === 'Q') {
        closeModal();
        return;
      }

      // TAB -> Toggles focus between environments and variables!
      if (key.name === 'tab') {
        if (this.activeFocusPane === 'envs') {
          this.activeFocusPane = 'vars';
          this.varsList.focus();
        } else {
          this.activeFocusPane = 'envs';
          this.envsList.focus();
        }
        this.updateBorders();
        return;
      }

      // Left arrow / 'h' -> switch to envs
      if (key.name === 'left' || (!key.ctrl && ch === 'h')) {
        this.activeFocusPane = 'envs';
        this.envsList.focus();
        this.updateBorders();
        return;
      }

      // Right arrow / 'l' -> switch to vars
      if (key.name === 'right' || (!key.ctrl && ch === 'l')) {
        this.activeFocusPane = 'vars';
        this.varsList.focus();
        this.updateBorders();
        return;
      }

      // 'A' -> Activate current environment
      if (ch === 'a' || ch === 'A') {
        this.activateSelectedEnv();
        return;
      }

      // 'E' -> Edit variable (when in vars pane)
      if ((ch === 'e' || ch === 'E') && this.activeFocusPane === 'vars') {
        this.promptEditVariable(this.selectedVarIndex);
        return;
      }

      // 'N' -> New variable or New env
      if (ch === 'n' || ch === 'N') {
        if (this.activeFocusPane === 'envs') {
          this.promptAddEnvironment();
        } else {
          this.promptAddVariable();
        }
        return;
      }

      // 'D' -> Delete variable
      if (ch === 'd' || ch === 'D' || key.name === 'delete') {
        if (this.activeFocusPane === 'vars') {
          this.deleteCurrentVariable();
        }
        return;
      }

      // 'S' -> Save to file
      if (ch === 's' || ch === 'S') {
        this.saveCurrentEnvironment();
        return;
      }
    };

    this.screen.on('keypress', this.keypressHandler);
  }

  private promptAddEnvironment() {
    const t = this.getTheme();

    const promptBox = blessed.prompt({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '50%',
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
    promptBox.input('Enter new environment name (e.g. UAT, Staging):', '', (err, envName) => {
      this.isPromptOpen = false;
      promptBox.destroy();
      this.envsList.focus();
      if (!err && envName && envName.trim()) {
        const clean = envName.trim();
        this.collection.environments.push({
          name: clean,
          variables: {}
        });
        this.selectedEnvIndex = this.collection.environments.length - 1;
        this.collection.activeEnvironment = clean;
        this.renderEnvsList();
        this.renderVarsList();
        (this.envsList as any).select(this.selectedEnvIndex);
        this.setStatus(`Created & activated new environment [${clean}]. Press [N] on variables to add vars.`);
        this.onUpdate();
      }
      this.screen.render();
    });
  }

  private promptEditVariable(varIndex: number) {
    const env = this.collection.environments[this.selectedEnvIndex];
    if (!env) return;

    const entries = Object.entries(env.variables);
    if (entries.length === 0) {
      this.promptAddVariable();
      return;
    }

    const target = entries[varIndex];
    if (!target) return;

    const [varName, currentValue] = target;
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
    promptBox.input(`Edit value for "${varName}":`, currentValue || '', (err, value) => {
      this.isPromptOpen = false;
      promptBox.destroy();
      this.varsList.focus();
      if (!err && value !== null && value !== undefined) {
        env.variables[varName] = value;
        if (this.collection.activeEnvironment === env.name) {
          this.collection.runtimeVariables[varName] = value;
        }
        this.renderVarsList();
        this.setStatus(`✔ Updated "${varName}" = "${value}". Press [S] to save to file.`);
        this.onUpdate();
      }
      this.screen.render();
    });
  }

  private promptAddVariable() {
    const env = this.collection.environments[this.selectedEnvIndex];
    if (!env) return;
    const t = this.getTheme();

    const namePrompt = blessed.prompt({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '60%',
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
    namePrompt.setFront();
    namePrompt.input(`[${env.name}] Enter variable name (e.g. API_HOST):`, '', (err, varName) => {
      namePrompt.destroy();
      if (!err && varName && varName.trim()) {
        const cleanName = varName.trim();
        const valPrompt = blessed.prompt({
          parent: this.screen,
          top: 'center',
          left: 'center',
          width: '60%',
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

        valPrompt.setFront();
        valPrompt.input(`Enter value for "${cleanName}":`, '', (vErr, varValue) => {
          this.isPromptOpen = false;
          valPrompt.destroy();
          this.varsList.focus();
          if (!vErr && varValue !== null && varValue !== undefined) {
            env.variables[cleanName] = varValue;
            if (this.collection.activeEnvironment === env.name) {
              this.collection.runtimeVariables[cleanName] = varValue;
            }
            this.renderVarsList();
            this.setStatus(`✔ Added "${cleanName}". Press [S] to save to file.`);
            this.onUpdate();
          }
          this.screen.render();
        });
      } else {
        this.isPromptOpen = false;
        this.varsList.focus();
        this.screen.render();
      }
    });
  }

  private deleteCurrentVariable() {
    const env = this.collection.environments[this.selectedEnvIndex];
    if (!env) return;

    const entries = Object.entries(env.variables);
    const target = entries[this.selectedVarIndex];
    if (!target) return;

    const [varName] = target;
    delete env.variables[varName];
    if (this.collection.activeEnvironment === env.name) {
      delete this.collection.runtimeVariables[varName];
    }

    this.renderVarsList();
    this.setStatus(`Deleted "${varName}". Press [S] to save to file.`);
    this.onUpdate();
  }

  private saveCurrentEnvironment() {
    const env = this.collection.environments[this.selectedEnvIndex];
    if (!env) return;

    try {
      const envDir = path.join(this.collection.path, 'environments');
      if (!fs.existsSync(envDir)) {
        fs.mkdirSync(envDir, { recursive: true });
      }

      const filePath = env.filePath || path.join(envDir, `${env.name}.bru`);

      const varList = Object.entries(env.variables).map(([name, value]) => {
        const isSecret = name.toLowerCase().includes('secret') || name.toLowerCase().includes('password');
        return {
          name,
          value,
          enabled: true,
          secret: isSecret
        };
      });

      const bruContent = envJsonToBruV2({ variables: varList });
      fs.writeFileSync(filePath, bruContent, 'utf8');

      env.filePath = filePath;
      this.setStatus(`✔ Successfully saved to ${path.basename(filePath)}!`);
    } catch (err: any) {
      this.setStatus(`✖ Failed to save: ${err.message}`);
    }
  }
}
