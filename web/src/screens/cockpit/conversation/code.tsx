import { highlightLine, type Language } from './highlight.ts';

/** Highlighted lines of code; the caller supplies the block that holds them. */
export function CodeLines({ text, language }: { text: string; language: Language }) {
  const lines = text.split('\n');
  return (
    <>
      {lines.map((line, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: lines have no identity
        <span key={i} className="code-line">
          <Tokens line={line} language={language} />
          {i < lines.length - 1 ? '\n' : ''}
        </span>
      ))}
    </>
  );
}

/** One line as syntax-coloured spans. */
export function Tokens({ line, language }: { line: string; language: Language }) {
  const tokens = highlightLine(line, language);
  return (
    <>
      {tokens.map((tok, i) =>
        tok.cls ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: tokens have no identity
          <span key={i} className={`sx-${tok.cls}`}>
            {tok.text}
          </span>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: tokens have no identity
          <span key={i}>{tok.text}</span>
        ),
      )}
    </>
  );
}
