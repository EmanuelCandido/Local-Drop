import { describe, expect, it } from 'vitest';
import { digest, metadataForFiles, ranges } from './transfer';

describe('chunk range expansion', () => {
  it('expands inclusive/exclusive ranges', () => {
    expect(ranges([
      { startInclusive: 2, endExclusive: 4 },
      { startInclusive: 7, endExclusive: 8 },
    ])).toEqual([2, 3, 7]);
  });
});

describe('HTTP-compatible hashing', () => {
  it('calculates SHA-256 without Web Crypto subtle', async () => {
    await expect(digest(new Blob(['abc']))).resolves.toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    );
  });
});

describe('offer metadata', () => {
  it('skips the whole-file hash so the file is read only once', () => {
    expect(metadataForFiles([new File(['abc'], 'a.bin')])).toEqual([
      { name: 'a.bin', size: 3, mimeType: 'application/octet-stream', sha256: '' },
    ]);
  });
});
