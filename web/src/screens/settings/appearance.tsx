import type { KeyboardEvent, ReactNode } from 'react';
import { Segmented } from '../../components/Segmented.tsx';
import { Switch } from '../../components/Switch.tsx';
import { currentLanguage, t } from '../../lib/i18n.ts';
import {
  type Appearance,
  CODE_FONTS,
  type CodeFont,
  SCHEMES,
  type Scheme,
  SEND_MODES,
  type SendMode,
  SIZES,
  type Size,
  UI_FONTS,
  type UiFont,
} from './prefs.ts';

/** The look of a window in one scheme, drawn from that scheme's own tokens. */
function Mini(props: { scheme: 'light' | 'dark'; half?: boolean }) {
  return (
    <div
      className={`mini${props.half ? ' half' : ''}`}
      data-scheme={props.scheme}
      aria-hidden="true"
    >
      <div className="ms">
        <i style={{ width: '70%' }} />
        <i style={{ width: '85%' }} />
        <i style={{ width: '60%' }} />
        <i style={{ width: '75%' }} />
      </div>
      <div className="mc">
        <i style={{ width: '70%' }} />
        <i style={{ width: '90%' }} />
        <i style={{ width: '55%' }} />
        <span className="cb" />
      </div>
    </div>
  );
}

function ThemeCard(props: { scheme: Scheme; label: string; on: boolean; onPick: () => void }) {
  const { scheme } = props;
  return (
    // biome-ignore lint/a11y/useSemanticElements: a card that is one radio of the Theme group
    <button
      type="button"
      role="radio"
      aria-checked={props.on}
      tabIndex={props.on ? 0 : -1}
      className={`thc${props.on ? ' on' : ''}`}
      onClick={props.onPick}
      data-testid={`settings.theme.${scheme}`}
    >
      <span className="thp">
        {scheme === 'system' ? (
          <>
            <Mini scheme="light" />
            <Mini scheme="dark" half />
          </>
        ) : (
          <Mini scheme={scheme} />
        )}
      </span>
      <span className="rl">
        <span className={`radio${props.on ? ' on' : ''}`} aria-hidden="true" />
        {props.label}
      </span>
    </button>
  );
}

const SEND_MODE_LABEL: Record<SendMode, () => string> = {
  queue: () => t('Queue'),
  steer: () => t('Steer'),
};

const SCHEME_LABEL: Record<Scheme, () => string> = {
  system: () => t('System'),
  light: () => t('Light'),
  dark: () => t('Dark'),
};

const UI_FONT_LABEL: Record<UiFont, () => string> = {
  inter: () => 'Inter',
  system: () => t('System'),
  geist: () => 'Geist',
};

const CODE_FONT_LABEL: Record<CodeFont, () => string> = {
  'jetbrains-mono': () => 'JetBrains Mono',
  system: () => t('System (SF Mono)'),
  'geist-mono': () => 'Geist Mono',
};

const SIZE_LABEL: Record<Size, () => string> = {
  small: () => t('Small'),
  default: () => t('Default'),
  large: () => t('Large'),
};

function Row(props: { name: string; hint?: string; children: ReactNode }) {
  return (
    <div className={`pr${props.hint ? ' hint' : ''}`}>
      <span className="nm">
        {props.name}
        {props.hint ? <span className="set-meta">{props.hint}</span> : null}
      </span>
      {props.children}
    </div>
  );
}

/** Settings > Appearance: theme, language and how text looks. Every choice is kept in the browser. */
export function AppearanceSection(props: {
  appearance: Appearance;
  onChange: (change: Partial<Appearance>) => void;
}) {
  const { appearance: a, onChange } = props;
  const move = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (step === undefined) return;
    e.preventDefault();
    const at = SCHEMES.indexOf(a.scheme);
    const next = SCHEMES[(at + step + SCHEMES.length) % SCHEMES.length];
    if (!next) return;
    onChange({ scheme: next });
    e.currentTarget.querySelectorAll<HTMLElement>('[role="radio"]')[SCHEMES.indexOf(next)]?.focus();
  };
  const language = a.language ?? currentLanguage();
  return (
    <>
      <h1>{t('Appearance')}</h1>
      <div className="sgrp" data-testid="settings.theme">
        <div className="lb" id="settings-theme">
          {t('Theme')}
        </div>
        <div className="themes" role="radiogroup" aria-labelledby="settings-theme" onKeyDown={move}>
          {SCHEMES.map((s) => (
            <ThemeCard
              key={s}
              scheme={s}
              label={SCHEME_LABEL[s]()}
              on={a.scheme === s}
              onPick={() => onChange({ scheme: s })}
            />
          ))}
        </div>
      </div>
      <div className="sgrp" data-testid="settings.language">
        <div className="lb">{t('Language')}</div>
        <div className="plist">
          <Row name={t('Interface language')}>
            <Segmented
              label={t('Interface language')}
              value={language}
              options={[
                { value: 'en', label: 'English', lang: 'en' },
                { value: 'zh-Hans', label: '简体中文', lang: 'zh-Hans' },
              ]}
              onChange={(value) => onChange({ language: value })}
              data-testid="settings.language.choice"
            />
          </Row>
        </div>
      </div>
      <div className="sgrp" data-testid="settings.sending">
        <div className="lb">{t('Send')}</div>
        <div className="plist">
          <Row
            name={t('Send while a turn runs')}
            hint={t('Enter does this while a turn runs; Alt+Enter does the other')}
          >
            <Segmented
              label={t('Send while a turn runs')}
              value={a.sendMode}
              options={SEND_MODES.map((value) => ({ value, label: SEND_MODE_LABEL[value]() }))}
              onChange={(sendMode) => onChange({ sendMode })}
              data-testid="settings.send-mode"
            />
          </Row>
        </div>
      </div>
      <div className="sgrp" data-testid="settings.text">
        <div className="lb">{t('Text')}</div>
        <div className="plist">
          <Row name={t('Interface font')}>
            <Segmented
              label={t('Interface font')}
              value={a.uiFont}
              options={UI_FONTS.map((value) => ({ value, label: UI_FONT_LABEL[value]() }))}
              onChange={(uiFont) => onChange({ uiFont })}
              data-testid="settings.ui-font"
            />
          </Row>
          <Row name={t('Code font')}>
            <Segmented
              label={t('Code font')}
              value={a.codeFont}
              options={CODE_FONTS.map((value) => ({ value, label: CODE_FONT_LABEL[value]() }))}
              onChange={(codeFont) => onChange({ codeFont })}
              data-testid="settings.code-font"
            />
          </Row>
          <Row name={t('Size')}>
            <Segmented
              label={t('Size')}
              value={a.size}
              options={SIZES.map((value) => ({ value, label: SIZE_LABEL[value]() }))}
              onChange={(size) => onChange({ size })}
              data-testid="settings.size"
            />
          </Row>
          <Row name={t('Full width')} hint={t('The conversation fills the main area')}>
            <Switch
              label={t('Full width')}
              hideLabel
              checked={a.wide}
              onChange={(wide) => onChange({ wide })}
              data-testid="settings.wide"
            />
          </Row>
          <Row name={t('Ligatures')} hint={t('In code and tool output')}>
            <Switch
              label={t('Ligatures')}
              hideLabel
              checked={a.ligatures}
              onChange={(ligatures) => onChange({ ligatures })}
              data-testid="settings.ligatures"
            />
          </Row>
        </div>
        {/* biome-ignore lint/a11y/useSemanticElements: a labelled region of sample text, not a form group */}
        <div
          className="tprev"
          role="group"
          aria-label={t('Preview')}
          data-testid="settings.preview"
        >
          <p className="text">
            {t(
              'The cursor moves by exactly the bytes written, so a resumed sync never repeats a line.',
            )}
          </p>
          <div className="pc mono">{'if (next !== size && cursor >= 0) => next  // != <= ==='}</div>
        </div>
      </div>
    </>
  );
}
