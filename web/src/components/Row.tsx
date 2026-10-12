import type { HTMLAttributes, ReactNode } from 'react';

type RowProps = Omit<HTMLAttributes<HTMLElement>, 'children'> & {
  leading?: ReactNode;
  trailing?: ReactNode;
  children: ReactNode;
  /** Row height and spacing: a tree row or a menu or palette row. */
  density?: 'tree' | 'menu';
  selected?: boolean;
  /** Offline or otherwise unavailable content, shown in the dim text colour. */
  dim?: boolean;
  /** Renders a button when set. */
  onPress?: () => void;
  muted?: boolean;
};

/** A list row: a leading slot, a label that ellipsizes, and a trailing slot (time, state, shortcut). */
export function Row(props: RowProps) {
  const {
    leading,
    trailing,
    children,
    density = 'tree',
    selected,
    dim,
    muted,
    onPress,
    className,
    ...rest
  } = props;
  const cls = `row row-${density}${selected ? ' selected' : ''}${dim ? ' dim' : ''}${muted ? ' muted' : ''}${className ? ` ${className}` : ''}`;
  const body = (
    <>
      {leading ? <span className="row-lead">{leading}</span> : null}
      <span className="row-label">{children}</span>
      {trailing ? <span className="row-trail">{trailing}</span> : null}
    </>
  );
  if (onPress) {
    return (
      <button type="button" className={cls} onClick={onPress} {...(rest as object)}>
        {body}
      </button>
    );
  }
  return (
    <div className={cls} {...rest}>
      {body}
    </div>
  );
}
