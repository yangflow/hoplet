const COPIED = [
  'boxSizing',
  'width',
  'paddingTop',
  'paddingRight',
  'paddingBottom',
  'paddingLeft',
  'borderTopWidth',
  'borderRightWidth',
  'borderBottomWidth',
  'borderLeftWidth',
  'fontFamily',
  'fontSize',
  'fontWeight',
  'fontStyle',
  'letterSpacing',
  'lineHeight',
  'tabSize',
  'textIndent',
  'textTransform',
  'wordSpacing',
] as const;

/**
 * Where the text at `index` starts, in pixels from the left edge of the field's offset parent: the field is
 * copied into a hidden block that lays out the text before `index`, and a marker after it is measured.
 */
export function xOfIndex(field: HTMLTextAreaElement, index: number): number {
  const mirror = document.createElement('div');
  const style = getComputedStyle(field);
  for (const name of COPIED) mirror.style[name] = style[name];
  mirror.style.position = 'absolute';
  mirror.style.visibility = 'hidden';
  mirror.style.whiteSpace = 'pre-wrap';
  mirror.style.overflowWrap = 'break-word';
  mirror.style.top = '0';
  mirror.style.left = '0';
  mirror.textContent = field.value.slice(0, index);
  const marker = document.createElement('span');
  marker.textContent = field.value.slice(index) || '.';
  mirror.appendChild(marker);
  (field.parentElement ?? document.body).appendChild(mirror);
  const x = marker.offsetLeft;
  mirror.remove();
  return field.offsetLeft + x;
}
