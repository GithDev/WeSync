import { describe, it, expect } from 'vitest';
import { homeTarget } from './home-route';

describe('homeTarget', () => {
  it('routes to /devices when there are no devices', () => {
    expect(homeTarget([])).toBe('/devices');
  });

  it('routes to /devices when only unpaired discovered peers are present', () => {
    // The getting-started guide lives on /devices; a nearby-but-unpaired peer
    // must not bounce a first-run user past it.
    expect(homeTarget([{ stPaired: false }, { stPaired: false }])).toBe('/devices');
  });

  it('routes to /folders once at least one device is paired', () => {
    expect(homeTarget([{ stPaired: false }, { stPaired: true }])).toBe('/folders');
  });
});
