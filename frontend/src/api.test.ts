import { beforeEach, describe, expect, it } from 'vitest';
import { createDeviceId, stableDeviceId } from './api';

describe('device identity', () => {
  beforeEach(() => localStorage.clear());

  it('creates an RFC 4122 version 4 identifier without randomUUID', () => {
    expect(createDeviceId()).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it('keeps the same identifier in local storage', () => {
    const first = stableDeviceId();
    expect(stableDeviceId()).toBe(first);
  });
});
