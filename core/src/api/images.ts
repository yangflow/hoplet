import { PROMPT_IMAGE_MAX_BYTES, type PromptImage, sniffImageType } from '@hoplet/shared';
import { ApiError } from './http.ts';

/**
 * Strict base64: the alphabet with padding only at the end, and a length that is a multiple of 4. One
 * character class, no group: megabytes of data must not grow the regular expression's backtracking stack.
 */
const BASE64 = /^[A-Za-z0-9+/]*={0,2}$/;
const isBase64 = (data: string) => data.length % 4 === 0 && BASE64.test(data);

/**
 * Checks an image uploaded for a prompt beyond its schema (docs/spec.md#hoplet, ADR 0017): base64 of at most
 * 4 MiB whose first bytes are the type it claims. A size beyond the limit is `too-large`; anything else is
 * `invalid-request` with `reason: images`.
 */
export function checkPromptImage(image: PromptImage): void {
  if (!isBase64(image.data)) throw new ApiError('invalid-request', { reason: 'images' });
  const bytes = Buffer.from(image.data, 'base64');
  if (bytes.length === 0) throw new ApiError('invalid-request', { reason: 'images' });
  if (bytes.length > PROMPT_IMAGE_MAX_BYTES) throw new ApiError('too-large');
  if (sniffImageType(bytes.subarray(0, 16)) !== image.mediaType) {
    throw new ApiError('invalid-request', { reason: 'images' });
  }
}
