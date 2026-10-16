/** A labelled on/off switch. */
export function Switch(props: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  /** The label names the switch for assistive technology only; the row beside it shows the words. */
  hideLabel?: boolean;
  'data-testid'?: string;
}) {
  const { label, checked, onChange, disabled, hideLabel, ...rest } = props;
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      className={`switch-row${checked ? ' on' : ''}`}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      {...rest}
    >
      <span className="switch" aria-hidden="true" />
      {hideLabel ? <span className="sr-only">{label}</span> : label}
    </button>
  );
}
