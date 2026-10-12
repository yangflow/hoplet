import type { HTMLAttributes } from 'react';

/** A bordered surface: recessed (`surface`), floating (`raised`) or an input well (`field`). */
export function Panel(
  props: HTMLAttributes<HTMLDivElement> & {
    tone?: 'surface' | 'raised' | 'field';
    focused?: boolean;
  },
) {
  const { tone = 'surface', focused, className, ...rest } = props;
  return (
    <div
      className={`panel panel-${tone}${focused ? ' focused' : ''}${className ? ` ${className}` : ''}`}
      {...rest}
    />
  );
}
