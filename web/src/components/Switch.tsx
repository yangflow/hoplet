/** A labelled on/off switch. */
export function Switch(props: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  'data-testid'?: string;
}) {
  const { label, checked, onChange, disabled, ...rest } = props;
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
      {label}
    </button>
  );
}
