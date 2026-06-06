import { useState, useEffect } from 'react';
import { wsService } from './wsService';

export type { WSState } from './wsService';

export function useWS() {
  const [state, setState] = useState(() => wsService.getState());
  useEffect(() => wsService.subscribe(setState), []);
  return state;
}
