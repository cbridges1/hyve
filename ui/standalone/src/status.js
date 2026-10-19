// How the console shows a cluster's state and expiry, for CAPI Clusters.
const PHASES = {
  Provisioned:  { label: 'Active', tone: 'good' },
  Provisioning: { label: 'Creating', tone: 'busy' },
  Pending:      { label: 'Pending', tone: 'busy' },
  Deleting:     { label: 'Deleting', tone: 'busy' },
  Failed:       { label: 'Failed', tone: 'bad' },
};

export function clusterStatus(c) {
  if (c.metadata?.deletionTimestamp) {
    return { label: 'Deleting', tone: 'busy' };
  }
  const p = PHASES[c.status?.phase];
  const notReady = (c.status?.conditions || []).find((x) => x.type === 'Available' && x.status === 'False');

  return p ? { ...p, title: notReady?.message || '' } : { label: c.status?.phase || 'Unknown', tone: 'idle' };
}

function duration(ms) {
  const minutes = Math.ceil(ms / 60000);

  if (minutes < 60) {
    return `${ minutes }m`;
  }
  const hours = Math.floor(minutes / 60);

  return hours < 24 ? `${ hours }h ${ minutes % 60 }m` : `${ Math.floor(hours / 24) }d ${ hours % 24 }h`;
}

// "Expires in 3h 20m", or nothing for a cluster that never expires.
export function expiry(at) {
  if (!at || at === 'never') {
    return null;
  }
  const t = Date.parse(at);

  if (Number.isNaN(t)) {
    return null;
  }
  const ms = t - Date.now();
  const title = `Scheduled for deletion at ${ new Date(t).toLocaleString() }`;

  return ms <= 0 ? { label: 'Expired — deleting', tone: 'busy', title } : { label: `Expires in ${ duration(ms) }`, tone: 'idle', title };
}
