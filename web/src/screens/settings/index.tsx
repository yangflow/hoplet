import { type ReactNode, useEffect, useRef } from 'react';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { t } from '../../lib/i18n.ts';
import '../../components/components.css';
import '../cockpit/shell.css';
import '../cockpit/conversation/conversation.css';
import './settings.css';
import { AppearanceSection } from './appearance.tsx';
import { type MachinesFixture, MachinesSection } from './machines.tsx';
import type { Appearance } from './prefs.ts';

/**
 * The sections of Settings, in the order of the list. A section is one entry here and one case in `Page`; the
 * Memory (#18) and Model providers (#16) pages join the same way.
 */
export const SECTIONS = [
  { id: 'appearance', label: 'Appearance', icon: () => Icon.contrast() },
  { id: 'machines', label: 'Machines', icon: () => Icon.laptop() },
] as const;

export type SectionId = (typeof SECTIONS)[number]['id'];

/** The section a `?settings=` value names; the first one when it names none. */
export function sectionOf(raw: string | null): SectionId {
  return SECTIONS.find((s) => s.id === raw)?.id ?? SECTIONS[0].id;
}

function Page(props: {
  section: SectionId;
  appearance: Appearance;
  onAppearance: (change: Partial<Appearance>) => void;
  machines: MachinesFixture | undefined;
}): ReactNode {
  switch (props.section) {
    case 'appearance':
      return <AppearanceSection appearance={props.appearance} onChange={props.onAppearance} />;
    case 'machines':
      return <MachinesSection {...(props.machines ? { fixture: props.machines } : {})} />;
  }
}

/** The Settings page: it replaces the window (no sidebar, no status bar); Esc and the back button leave it. */
export function Settings(props: {
  section: SectionId;
  onSection: (section: SectionId) => void;
  onBack: () => void;
  appearance: Appearance;
  onAppearance: (change: Partial<Appearance>) => void;
  machines?: MachinesFixture;
}) {
  const { onBack } = props;
  const title = useRef<HTMLSpanElement>(null);
  useEffect(() => title.current?.focus(), []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !e.defaultPrevented && !e.isComposing) onBack();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onBack]);
  return (
    <div className="settings" data-testid="settings.page">
      <main className="main">
        <header className="top">
          <IconButton
            label={t('Back to the conversation (Esc)')}
            icon={Icon.back()}
            onClick={onBack}
            data-testid="settings.back"
          />
          <span className="title" ref={title} tabIndex={-1}>
            {t('Settings')}
          </span>
        </header>
        <div className="set">
          <nav className="snav" aria-label={t('Settings sections')}>
            {SECTIONS.map((s) => (
              <button
                key={s.id}
                type="button"
                className={`ni${props.section === s.id ? ' on' : ''}`}
                {...(props.section === s.id ? { 'aria-current': 'page' as const } : {})}
                onClick={() => props.onSection(s.id)}
                data-testid={`settings.nav.${s.id}`}
              >
                {s.icon()}
                {t(s.label)}
              </button>
            ))}
          </nav>
          <div className="spage">
            <div className="in">
              <Page
                section={props.section}
                appearance={props.appearance}
                onAppearance={props.onAppearance}
                machines={props.machines}
              />
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}
