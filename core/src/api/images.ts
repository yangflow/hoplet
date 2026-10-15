import {
  PROMPT_IMAGE_MAX_BYTES,
  PROMPT_IMAGES_MAX_BYTES,
  type PromptImage,
  sniffImageType,
} from '@hoplet/shared';
import { ApiError } from './http.ts';

/**
 * Strict base64: the alphabet with padding only at the end, and a length that is a multiple of 4. One
 * character class, no group: megabytes of data must not grow the regular expression's backtracking stack.
 */
const BASE64 = /^[A-Za-z0-9+/]*={0,2}$/;
const isBase64 = (data: string) => data.length % 4 === 0 && BASE64.test(data);

/**
 * Checks the images of a prompt beyond their schema (docs/spec.md#hoplet, ADR 0017): each is base64 of at most
 * 4 MiB whose first bytes are the type it claims, and together they are at most 8 MiB. A size beyond the limits
 * is `too-large`; anything else is `invalid-request` with `reason: images`.
 */
export function checkPromptImages(images: readonly PromptImage[] | undefined): void {
  let total = 0;
  for (const image of images ?? []) {
    if (!isBase64(image.data)) throw new ApiError('invalid-request', { reason: 'images' });
    const bytes = Buffer.from(image.data, 'base64');
    if (bytes.length === 0) throw new ApiError('invalid-request', { reason: 'images' });
    if (bytes.length > PROMPT_IMAGE_MAX_BYTES) throw new ApiError('too-large');
    total += bytes.length;
    if (total > PROMPT_IMAGES_MAX_BYTES) throw new ApiError('too-large');
    if (sniffImageType(bytes.subarray(0, 16)) !== image.mediaType) {
      throw new ApiError('invalid-request', { reason: 'images' });
    }
  }
}
