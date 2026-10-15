import { RANGES, useRange, type Range } from '../hooks/useRange';
import { Tabs, TabsList, TabsTab } from '../primitives/tabs';

/** 1h · 24h · 7d, for everything on the page that counts over time. */
export function RangeTabs() {
  const { value, setRange } = useRange();
  return (
    <Tabs value={value} onValueChange={(v) => setRange(v as Range)}>
      <TabsList>
        {RANGES.map((r) => (
          <TabsTab key={r.value} value={r.value}>
            {r.value}
          </TabsTab>
        ))}
      </TabsList>
    </Tabs>
  );
}
