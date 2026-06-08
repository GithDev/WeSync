import { describe, it, expect } from 'vitest';
import { SENDER_DISPLAY } from './DirectionPicker';
import { FolderDirection } from '../../types/enums';

describe('SENDER_DISPLAY', () => {
  it('SendOnly shows receive-files semantics', () => {
    const e = SENDER_DISPLAY[FolderDirection.SendOnly]!;
    expect(e.title).toContain('Receive files');
    expect(e.arrow).toBe('→');
  });

  it('SendReceive shows two-way semantics', () => {
    const e = SENDER_DISPLAY[FolderDirection.SendReceive]!;
    expect(e.title).toContain('Two-way');
    expect(e.arrow).toBe('↔');
  });

  it('covers every FolderDirection — no runtime fallback needed', () => {
    for (const dir of Object.values(FolderDirection)) {
      expect(SENDER_DISPLAY[dir], `missing entry for ${dir}`).toBeDefined();
    }
  });
});
