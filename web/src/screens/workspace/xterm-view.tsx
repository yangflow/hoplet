import { useEffect, useRef } from 'react';
import { t } from '../../lib/i18n.ts';
import { terminalTheme } from '../../lib/terminal.ts';
import type { TerminalSink } from './use-terminal.ts';

const FONT_SIZE = 12.5;
const LINE_HEIGHT = 1.6;

function currentScheme(): 'light' | 'dark' {
  const set = document.documentElement.dataset.scheme;
  if (set === 'light' || set === 'dark') return set;
  return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

function readToken(token: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(token).trim();
}

/**
 * The terminal emulator. xterm.js and its fit addon load on first use, so a window that never opens a terminal
 * does not carry them. Nothing is kept: the buffer lives in the emulator and goes with it.
 */
export function XtermView(props: {
  setSink: (sink: TerminalSink | null) => void;
  onKeys: (bytes: Uint8Array) => void;
  onSize: (cols: number, rows: number) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const callbacks = useRef(props);
  callbacks.current = props;

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    let disposed = false;
    let cleanup: (() => void) | null = null;

    void (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import('@xterm/xterm'),
        import('@xterm/addon-fit'),
        import('@xterm/xterm/css/xterm.css'),
      ]);
      if (disposed) return;
      const term = new Terminal({
        fontFamily: readToken('--font-mono'),
        fontSize: FONT_SIZE,
        lineHeight: LINE_HEIGHT,
        cursorBlink: true,
        scrollback: 5000,
        allowProposedApi: false,
        theme: terminalTheme(readToken, currentScheme()),
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(el);
      const refit = () => {
        try {
          fit.fit();
        } catch {
          /* the view is hidden or has no size yet */
        }
      };
      refit();
      callbacks.current.onSize(term.cols, term.rows);

      const encoder = new TextEncoder();
      const data = term.onData((text) => callbacks.current.onKeys(encoder.encode(text)));
      const size = term.onResize(({ cols, rows }) => callbacks.current.onSize(cols, rows));
      callbacks.current.setSink({
        write: (bytes) => term.write(bytes),
        reset: () => term.reset(),
      });

      const retheme = () => {
        term.options.theme = terminalTheme(readToken, currentScheme());
      };
      const observer = new MutationObserver(retheme);
      observer.observe(document.documentElement, {
        attributes: true,
        attributeFilter: ['data-scheme'],
      });
      const media = window.matchMedia?.('(prefers-color-scheme: light)');
      media?.addEventListener('change', retheme);
      const resize = new ResizeObserver(refit);
      resize.observe(el);
      document.fonts?.ready.then(() => {
        if (disposed) return;
        term.options.fontFamily = readToken('--font-mono');
        refit();
      });
      term.focus();

      cleanup = () => {
        observer.disconnect();
        media?.removeEventListener('change', retheme);
        resize.disconnect();
        data.dispose();
        size.dispose();
        callbacks.current.setSink(null);
        term.dispose();
      };
    })();

    return () => {
      disposed = true;
      cleanup?.();
    };
  }, []);

  return (
    <section
      ref={host}
      className="ws-xterm"
      aria-label={t('Terminal')}
      data-testid="workspace.terminal-view"
    />
  );
}
