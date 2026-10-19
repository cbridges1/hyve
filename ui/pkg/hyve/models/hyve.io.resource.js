import SteveModel from '@shell/plugins/steve/steve-class';
import { describe, kindOf } from '../utils/resources';

// A shared Resource: a Fleet spec templates and extras refer to by name.
export default class Resource extends SteveModel {
  get fleetKind() {
    return kindOf(this.spec || {});
  }

  get sourceDisplay() {
    return describe(this.spec || {});
  }

  get secretsDisplay() {
    return (this.spec?.secrets || []).join(', ');
  }
}
