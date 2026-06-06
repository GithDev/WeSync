interface Props {
  active: boolean;
}

const ARC_RADII = [110, 185, 260, 335];
const BG = 'rgb(248,250,252)';

export function RadarBackground({ active }: Props) {
  const arcColor = active ? 'rgba(99,140,255,0.18)' : 'rgba(150,150,150,0.08)';
  const sweep = {
    animationName: 'radar-half-spin',
    animationDuration: '5s',
    animationTimingFunction: 'linear',
    animationIterationCount: 'infinite',
  } as const;

  return (
    <>
      {/* Range rings + sweep line */}
      <svg
        className="absolute inset-0 w-full h-full pointer-events-none"
        viewBox="0 0 520 340"
        preserveAspectRatio="xMidYMax slice"
      >
        {ARC_RADII.map((r) => (
          <path
            key={r}
            d={`M ${260 - r} 340 A ${r} ${r} 0 0 1 ${260 + r} 340`}
            fill="none"
            stroke={arcColor}
            strokeWidth="1.5"
            style={{ transition: 'stroke 0.6s ease' }}
          />
        ))}
        {active && (
          <line
            x1="260"
            y1="340"
            x2="260"
            y2="5"
            stroke="rgba(99,140,255,0.7)"
            strokeWidth="1.5"
            style={{ transformOrigin: '260px 340px', ...sweep }}
          />
        )}
      </svg>

      {/* Conic sweep afterglow — viewport-sized so it covers all rings */}
      {active && (
        <div
          className="absolute pointer-events-none"
          style={{
            width: 'max(220vw, 160vh)',
            height: 'max(220vw, 160vh)',
            bottom: 'calc(-1 * max(110vw, 80vh))',
            left: 'calc(50% - max(110vw, 80vh))',
            borderRadius: '50%',
            background: `conic-gradient(${BG} 62%, rgba(248,250,252,0) 82%, rgba(99,140,255,0.55) 98%, ${BG} 100%)`,
            ...sweep,
          }}
        />
      )}
    </>
  );
}
