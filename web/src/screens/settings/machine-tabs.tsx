import type { DeviceInfo } from '@hoplet/shared';
import { type KeyboardEvent, useRef } from 'react';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import { t } from '../../lib/i18n.ts';
import { machineLabel, systemOf } from '../../lib/system.ts';

/** The id of a machine's tab, for the `aria-labelledby` of its panel. */
export function machineTabId(idPrefix: string, deviceId: string): string {
  return `${idPrefix}-tab-${deviceId}`;
}

/** The id of the panel the selected tab controls. */
export function machinePanelId(idPrefix: string): string {
  return `${idPrefix}-panel`;
}

/** A tab per machine, led by its system's mark: the arrow keys, Home and End move between them and select. */
export function MachineTabs(props: {
  devices: readonly DeviceInfo[];
  selected: string;
  idPrefix: string;
  /** False while no panel is on screen (no tab controls anything). */
  panel?: boolean;
  onSelect: (deviceId: string) => void;
  testid: string;
}) {
  const list = useRef<HTMLDivElement>(null);

  const onKeyDown = (e: KeyboardEvent) => {
    const tabs = [...(list.current?.querySelectorAll<HTMLElement>('[role="tab"]') ?? [])];
    const at = tabs.indexOf(document.activeElement as HTMLElement);
    if (at < 0) return;
    const to =
      e.key === 'ArrowRight'
        ? at + 1
        : e.key === 'ArrowLeft'
          ? at - 1
          : e.key === 'Home'
            ? 0
            : e.key === 'End'
              ? -1
              : null;
    if (to === null) return;
    e.preventDefault();
    const next = tabs[(to + tabs.length) % tabs.length];
    next?.focus();
    const deviceId = next?.dataset.device;
    if (deviceId) props.onSelect(deviceId);
  };

  return (
    <div
      ref={list}
      className="mp-tabs"
      role="tablist"
      aria-label={t('Machines')}
      onKeyDown={onKeyDown}
      data-testid={props.testid}
    >
      {props.devices.map((d) => {
        const on = d.deviceId === props.selected;
        return (
          <button
            key={d.deviceId}
            type="button"
            role="tab"
            id={machineTabId(props.idPrefix, d.deviceId)}
            className="mp-tab"
            aria-selected={on}
            aria-controls={on && props.panel !== false ? machinePanelId(props.idPrefix) : undefined}
            tabIndex={on ? 0 : -1}
            data-device={d.deviceId}
            title={machineLabel(d.name, d.system, d.connected)}
            onClick={() => props.onSelect(d.deviceId)}
          >
            <MachineIcon kind={systemOf(d.system?.os)} online={d.connected} />
            {d.name}
          </button>
        );
      })}
    </div>
  );
}
