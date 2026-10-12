/**
 * Elision (ADR 0007): a block whose encoded size exceeds 8 KiB is served as a reference with a short
 * text preview; `GET /api/sessions/:id/blocks/:line/:block` returns the full block.
 */
import { type Block, blockRef, type Entry, type FullBlock } from '@hoplet/shared';

export const ELIDE_ABOVE_BYTES = 8 * 1024;
export const PREVIEW_BYTES = 1024;

/** The text a block can be previewed or searched by; images have none. */
export function blockText(block: FullBlock): string {
  switch (block.type) {
    case 'text':
    case 'thinking':
      return block.text;
    case 'tool-call':
      return JSON.stringify(block.input) ?? '';
    case 'tool-result':
      return block.content;
    case 'other':
      return block.text ?? '';
    case 'image':
      return '';
  }
}

/** The longest prefix of `text` that fits `limit` UTF-8 bytes without cutting a character. */
export function utf8Prefix(text: string, limit: number): string {
  // `limit` characters are at least `limit` bytes unless the text is shorter, so only a bounded head is measured.
  const head = text.length > limit ? text.slice(0, limit) : text;
  if (Buffer.byteLength(head) <= limit) return head;
  let end = head.length;
  while (end > 0 && Buffer.byteLength(head.slice(0, end)) > limit) end--;
  // A cut between the halves of a surrogate pair would leave an unpaired surrogate.
  const last = head.charCodeAt(end - 1);
  if (last >= 0xd800 && last <= 0xdbff) end--;
  return head.slice(0, end);
}

/** The encoded (JSON) size of a block, which decides whether it is elided. */
export function encodedSize(block: FullBlock): number {
  return Buffer.byteLength(JSON.stringify(block));
}

export function elideBlock(block: FullBlock, lineNo: number, index: number): Block {
  const bytes = encodedSize(block);
  if (bytes <= ELIDE_ABOVE_BYTES) return block;
  return {
    elided: true,
    type: block.type,
    bytes,
    preview: block.type === 'image' ? '' : utf8Prefix(blockText(block), PREVIEW_BYTES),
    ref: blockRef(lineNo, index),
  };
}

export function elideEntry(entry: Entry): Entry {
  return {
    ...entry,
    blocks: entry.blocks.map((block, i) =>
      'elided' in block ? block : elideBlock(block, entry.lineNo, i),
    ),
  };
}
