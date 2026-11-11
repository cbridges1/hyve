// A Deployment's source, as the resource form edits it: Fleet's own
// HelmOp, Bundle or GitRepo spec, plus a namespace and the Secrets it
// copies (the CLI's internal/resources Entry). lib/delivery.ts makes the
// Fleet object.
import { TYPES } from './types';

export const FLEET = {
  HELM_OP: TYPES.FLEET_HELM_OP,
  GIT_REPO: TYPES.FLEET_GIT_REPO,
  BUNDLE: TYPES.FLEET_BUNDLE,
  BUNDLE_DEPLOYMENT: TYPES.FLEET_BUNDLE_DEPLOYMENT,
  CLUSTER: TYPES.FLEET_CLUSTER,
};

export interface Entry {
  name?: string;
  namespace?: string;
  secrets?: string[];
  ref?: string;
  refNamespace?: string;
  values?: any;
  helmOp?: any;
  bundle?: any;
  gitRepo?: any;
}

export function kindOf(e: Entry): string {
  return e.helmOp ? 'HelmOp' : e.bundle ? 'Bundle' : e.gitRepo ? 'GitRepo' : e.ref ? 'Ref' : '';
}

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export function validate(e: Entry): string {
  const name = e.name || '';

  if (!NAME_RE.test(name) || name.length > 40) {
    return `Resource name "${ name }": lowercase letters, digits and dashes, at most 40`;
  }
  const kinds = [e.helmOp, e.bundle, e.gitRepo, e.ref].filter(Boolean).length;

  if (kinds !== 1) {
    return `Resource ${ name }: give exactly one of a Helm chart, git repo or existing resource`;
  }
  if (e.helmOp) {
    const h = e.helmOp.helm || {};

    if (!h.chart && !(h.repo || '').startsWith('oci://')) {
      return `Resource ${ name }: chart is required (or an oci:// repository)`;
    }
  }
  if (e.gitRepo && !e.gitRepo.repo) {
    return `Resource ${ name }: git repo is required`;
  }
  if (e.gitRepo && e.secrets?.length) {
    return `Resource ${ name }: a git repo can't copy secrets; list them in its fleet.yaml`;
  }

  return '';
}

// A Fleet state as a badge colour.
export function stateColor(state: string): string {
  if (!state || state === '-') {
    return 'text-muted';
  }
  if (state === 'Ready') {
    return 'text-success';
  }
  if (/^(Error|ErrApplied|Modified|OutOfSync|NotReady)|^[A-Z][a-zA-Z]*: /.test(state)) {
    return 'text-error';
  }

  return 'text-warning';
}
