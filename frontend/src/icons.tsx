import { PATHS } from "./icons.generated";

/**
 * The Chat page's icon set: Tabler's outline glyphs from the one icon registry
 * (design/icons.json, written into icons.generated.ts), inlined so the page stays
 * dependency-free. The path strings are static constants, never data. The default
 * stroke is the tier's (--icon-stroke: 1.5 on a pointer, 2 on a touch screen).
 */

export type IconName = keyof typeof PATHS;

export function Icon({ name, size = 14, stroke }: { name: IconName; size?: number; stroke?: number }) {
  return (
    <svg
      className="ic"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={stroke}
      style={stroke === undefined ? { strokeWidth: "var(--icon-stroke)" } : undefined}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      dangerouslySetInnerHTML={{ __html: PATHS[name] }}
    />
  );
}

/** The Stop glyph: a filled rounded square. */
export function StopIcon({ size = 12 }: { size?: number }) {
  return (
    <svg className="ic" width={size} height={size} viewBox="0 0 12 12" aria-hidden>
      <rect width="10" height="10" x="1" y="1" rx="2" fill="currentColor" />
    </svg>
  );
}
