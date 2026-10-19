import { describe, expect, it } from 'vitest';
import { machineLabel, systemName, systemOf } from '../src/lib/system.ts';

describe('the system of a machine', () => {
  it('maps the GOOS an agent announces to a mark', () => {
    expect(systemOf('darwin')).toBe('macos');
    expect(systemOf('windows')).toBe('windows');
    expect(systemOf('linux')).toBe('linux');
  });

  it('gives any other system, and an agent that announced none, the generic mark', () => {
    for (const os of ['freebsd', 'plan9', '', 'Darwin', null, undefined]) {
      expect(systemOf(os)).toBe('other');
      expect(systemName(os)).toBeNull();
    }
  });

  it('names the three systems', () => {
    expect(systemName('darwin')).toBe('macOS');
    expect(systemName('windows')).toBe('Windows');
    expect(systemName('linux')).toBe('Linux');
  });

  it('builds the accessible name from machine, system and presence', () => {
    const mac = { os: 'darwin', arch: 'arm64' };
    expect(machineLabel('MacBook Pro', mac, true)).toBe('MacBook Pro, macOS, online');
    expect(machineLabel('office-mini', { os: 'windows', arch: 'amd64' }, false)).toBe(
      'office-mini, Windows, offline',
    );
  });

  it('leaves the system out of the name when it is unknown', () => {
    expect(machineLabel('lab-box', null, true)).toBe('lab-box, online');
    expect(machineLabel('lab-box', { os: 'freebsd', arch: 'amd64' }, false)).toBe(
      'lab-box, offline',
    );
  });
});
