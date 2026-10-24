// The next run of a standard 5-field cron schedule (minute hour
// day-of-month month day-of-week), in the browser's time zone — as the CLI
// evaluates it in the local one. Supports *, lists, ranges and steps. A
// leading CRON_TZ=... is accepted; only UTC is honoured exactly.

function parseField(field: string, min: number, max: number): Set<number> {
  const out = new Set<number>();

  field.split(',').forEach((part) => {
    const [range, stepStr] = part.split('/');
    const step = stepStr ? parseInt(stepStr, 10) : 1;
    let lo = min;
    let hi = max;

    if (range !== '*') {
      const [a, b] = range.split('-');

      lo = parseInt(a, 10);
      hi = b !== undefined ? parseInt(b, 10) : (stepStr ? max : lo);
    }
    if (isNaN(lo) || isNaN(hi) || isNaN(step) || lo < min || hi > max || step < 1) {
      throw new Error(`invalid cron field "${ field }"`);
    }
    for (let v = lo; v <= hi; v += step) {
      out.add(v === 7 && max === 7 ? 0 : v);
    }
  });

  return out;
}

export function nextRun(schedule: string, from: Date): Date {
  let s = schedule.trim();
  let utc = false;
  const tz = s.match(/^(CRON_)?TZ=(\S+)\s+/);

  if (tz) {
    utc = tz[2] === 'UTC' || tz[2] === 'Etc/UTC';
    s = s.slice(tz[0].length);
  }
  const fields = s.split(/\s+/);

  if (fields.length !== 5) {
    throw new Error(`schedule "${ schedule }": want 5 fields`);
  }
  const [mins, hours, doms, months, dows] = [
    parseField(fields[0], 0, 59), parseField(fields[1], 0, 23), parseField(fields[2], 1, 31),
    parseField(fields[3], 1, 12), parseField(fields[4], 0, 7),
  ];
  const domStar = fields[2] === '*';
  const dowStar = fields[4] === '*';
  const d = new Date(from.getTime());

  d.setSeconds(0, 0);
  d.setTime(d.getTime() + 60000);
  const get = (k: string) => (utc ? (d as any)[`getUTC${ k }`]() : (d as any)[`get${ k }`]());

  for (let i = 0; i < 366 * 24 * 60; i++) {
    const dayOk = domStar && dowStar ? true : (domStar ? dows.has(get('Day')) : (dowStar ? doms.has(get('Date')) : (doms.has(get('Date')) || dows.has(get('Day')))));

    if (months.has(get('Month') + 1) && dayOk && hours.has(get('Hours')) && mins.has(get('Minutes'))) {
      return d;
    }
    d.setTime(d.getTime() + 60000);
  }
  throw new Error(`schedule "${ schedule }" never runs`);
}
