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

  private modal?: blessed.Widgets.BoxElement;
  private selectedEnvIndex: number = 0;
  private selectedVarIndex: number = 0;
  private activeFocusPane: 'envs' | 'vars' = 'envs';

  // Widgets
  private envsList!: blessed.Widgets.ListElement;
  private varsList!: blessed.Widgets.ListElement;
  private statusBox!: blessed.Widgets.BoxElement;
  private footerBar!: blessed.Widgets.BoxElement;

  constructor(
    screen: blessed.Widgets.Screen,
    collection: BrunoCollection,
    themeManager: ThemeManager,
    onUpdate: () => void
  ) {
    this.screen = screen;
    this.collection = collection;
    this.themeManager = themeManager;
    this.onUpdate = onUpdate;
  }

  private getTheme(): ThemeColors {
    return this.themeManager.getCurrent();
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
      right: 1,
      height: 2,
      content: ` {bold}{${t.accent}-fg}🌍 ENVIRONMENT & VARIABLES MANAGER{/}  ` +
               `{${t.subtext}-fg}Active:{/} {bold}{${t.options}-fg}[${this.collection.activeEnvironment || 'None'}]{/}  ` +
               `{right}{${t.subtext}-fg}[Tab to switch panel | Esc to close]{/} `,
      tags: true
    });

    // Left Pane: Environments List (width 32%)
    this.envsList = blessed.list({
      parent: this.modal,
      top: 2,
      left: 1,
      width: '32%',
      bottom: 4,
      border: { type: 'line' },
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
        item: { hover: { bg: t.surfaceHover } }
      },
      scrollbar: {
        ch: ' ',
        track: { bg: t.surface },
        style: { inverse: true }
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
        item: { hover: { bg: t.surfaceHover } }
      },
      scrollbar: {
        ch: ' ',
        track: { bg: t.surface },
        style: { inverse: true }
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
      const bullet = isCur ? `{${t.get}-fg}●{/} {bold}${e.name}{/}` : `  ${e.name}`;
      const count = Object.keys(e.variables).length;
      return `${bullet} {${t.subtext}-fg}(${count}){/}`;
    });

    items.push(`{${t.put}-fg}[+ New Environment]{/}`);

    this.envsList.setItems(items);
  }

  private renderVarsList() {
    const t = this.getTheme();
    const env = this.collection.environments[this.selectedEnvIndex];
    if (!env) {
      this.varsList.setItems(['No environment selected']);
      return;
    }

    const entries = Object.entries(env.variables);
    if (entries.length === 0) {
      this.varsList.setItems([
        `{${t.subtext}-fg}No variables in [${env.name}]. Press [N] to add a variable.{/}`
      ]);
      return;
    }

    const items = entries.map(([name, val], idx) => {
      const isSecret = name.toLowerCase().includes('secret') || name.toLowerCase().includes('password');
      let displayVal = val;
      if (isSecret && val && val.length > 4) {
        displayVal = val.slice(0, 3) + '••••••••' + val.slice(-3);
      }
      if (!val) {
        displayVal = '{#6c7086-fg}(empty){/}';
      }

      const num = String(idx + 1).padStart(2, ' ');
      const typeBadge = isSecret ? `{${t.delete}-fg}[SECRET]{/}` : `{${t.put}-fg}[TEXT]{/}`;
      const nameCol = `{bold}{${t.options}-fg}${name.padEnd(26)}{/}`;

      return ` ${num}. ${typeBadge} ${nameCol} = {${t.get}-fg}${displayVal}{/}`;
    });

    this.varsList.setItems(items);
  }

  private renderFooter() {
    const t = this.getTheme();
    const env = this.collection.environments[this.selectedEnvIndex];
    const isCur = env?.name === this.collection.activeEnvironment;

    if (this.activeFocusPane === 'envs') {
      this.footerBar.setContent(
        ` {bold}[Enter/Click]{/} ${isCur ? '{#6c7086-fg}(Already Active){/}' : '{#a6e3a1-fg}ACTIVATE ENVIRONMENT{/}'}  ` +
        `{bold}[Tab]{/} Edit Variables  ` +
        `{bold}[N]{/} New Env  ` +
        `{bold}[Esc]{/} Close `
      );
    } else {
      this.footerBar.setContent(
        ` {bold}[Enter/Click]{/} {#cba6f7-fg}EDIT VALUE{/}  ` +
        `{bold}[Tab]{/} Switch to Envs  ` +
        `{bold}[N]{/} Add Var  ` +
        `{bold}[D]{/} Del Var  ` +
        `{bold}[S]{/} Save File  ` +
        `{bold}[Esc]{/} Close `
      );
    }
  }

  private setStatus(msg: string) {
    const t = this.getTheme();
    this.statusBox.setContent(` {bold}{${t.accent}-fg}Info:{/} ${msg}`);
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

  private bindEvents() {
    const closeModal = () => {
      if (this.modal) {
        this.modal.destroy();
        this.modal = undefined;
        this.onUpdate();
        this.screen.render();
      }
    };

    // 1. Envs List: Selection preview
    this.envsList.on('select item', (_item: any, index: number) => {
      if (index < this.collection.environments.length) {
        this.selectedEnvIndex = index;
        this.renderVarsList();
        this.renderFooter();
        this.screen.render();
      }
    });

    // 2. Envs List: Enter / Click -> Activate Environment or Add New Env!
    this.envsList.on('select', (_item: any, index: number) => {
      const envs = this.collection.environments;
      if (index === envs.length) {
        // [+ New Environment]
        this.promptAddEnvironment();
      } else {
        const selected = envs[index];
        if (selected) {
          this.collection.activeEnvironment = selected.name;
          this.renderEnvsList();
          this.renderFooter();
          this.setStatus(`✔ Activated environment: [${selected.name}]`);
          this.onUpdate();
        }
      }
    });

    // Click on Envs List switches focus
    this.envsList.on('click', () => {
      this.activeFocusPane = 'envs';
      this.updateBorders();
    });

    // Click on Vars List switches focus
    this.varsList.on('click', () => {
      this.activeFocusPane = 'vars';
      this.updateBorders();
    });

    // 3. Vars List: Selection
    this.varsList.on('select item', (_item: any, index: number) => {
      this.selectedVarIndex = index;
    });

    // 4. Vars List: Enter / Click -> Edit Variable Value!
    this.varsList.on('select', (_item: any, index: number) => {
      this.promptEditVariable(index);
    });

    // 5. Global Modal Keys
    this.modal!.key(['escape'], () => {
      closeModal();
    });

    // Tab key toggles focus between Envs list and Vars list
    this.modal!.key(['tab'], () => {
      if (this.activeFocusPane === 'envs') {
        this.activeFocusPane = 'vars';
        this.varsList.focus();
      } else {
        this.activeFocusPane = 'envs';
        this.envsList.focus();
      }
      this.updateBorders();
    });

    // Hotkey [N]: New Var or New Env based on focus
    this.modal!.key(['n', 'N'], () => {
      if (this.activeFocusPane === 'envs') {
        this.promptAddEnvironment();
      } else {
        this.promptAddVariable();
      }
    });

    // Hotkey [D]: Delete Variable
    this.modal!.key(['d', 'D'], () => {
      if (this.activeFocusPane === 'vars') {
        this.deleteCurrentVariable();
      }
    });

    // Hotkey [S]: Save current environment to file
    this.modal!.key(['s', 'S'], () => {
      this.saveCurrentEnvironment();
    });
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

    promptBox.setFront();
    promptBox.input('Enter new environment name (e.g. UAT, Staging):', '', (err, envName) => {
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
    const target = entries[varIndex];
    if (!target) return;

    const [varName, currentValue] = target;
    const t = this.getTheme();

    const promptBox = blessed.prompt({
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

    promptBox.setFront();
    promptBox.input(`Edit value for "${varName}":`, currentValue || '', (err, value) => {
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
