import { Fragment } from 'react';

/** A sentence whose backtick-quoted parts are set as code chips. */
export function CodeText({ text }: { text: string }) {
  let at = 0;
  return (
    <>
      {text.split('`').map((part, i) => {
        // Each part is keyed by where it starts in the sentence, which a fixed string never changes.
        const key = at;
        at += part.length + 1;
        // Odd parts sit between two backticks.
        return i % 2 === 1 ? (
          <code key={key} className="ws-mc">
            {part}
          </code>
        ) : (
          <Fragment key={key}>{part}</Fragment>
        );
      })}
    </>
  );
}
