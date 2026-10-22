import { randomBytes } from 'node:crypto';

const ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
const RANDOM_MASK = (1n << 80n) - 1n;

function encode(value: bigint, length: number): string {
  let out = '';
  let rest = value;
  for (let i = 0; i < length; i++) {
    out = ALPHABET[Number(rest & 31n)] + out;
    rest >>= 5n;
  }
  return out;
}

/** Makes ULIDs that sort in the order they are made, also within one millisecond. */
export function createUlid(now: () => number): () => string {
  let lastTime = -1;
  let lastRandom = 0n;
  return () => {
    let time = now();
    if (time <= lastTime) {
      time = lastTime;
      lastRandom = (lastRandom + 1n) & RANDOM_MASK;
    } else {
      lastTime = time;
      lastRandom = BigInt(`0x${randomBytes(10).toString('hex')}`);
    }
    return encode(BigInt(time), 10) + encode(lastRandom, 16);
  };
}
