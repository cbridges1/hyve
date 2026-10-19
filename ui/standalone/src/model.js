// The stand-in for Rancher's SteveModel: a Kubernetes object with save()
// and remove(). The extension's own models (../pkg/hyve/models) extend it.
import { request, urlFor } from './api';

let ctx = null;

// The store, router and t() models reach through; set once at start.
export function setModelContext(c) {
  ctx = c;
}

export default class Model {
  constructor(data, type) {
    Object.assign(this, JSON.parse(JSON.stringify(data)));
    delete this.type;
    Object.defineProperty(this, 'type', {
      value: type, enumerable: false, writable: true, configurable: true
    });
  }

  get id() {
    const m = this.metadata || {};

    return m.namespace ? `${ m.namespace }/${ m.name }` : m.name;
  }

  get schema() {
    return ctx.store.getters['management/schemaFor'](this.type);
  }

  get nameDisplay() {
    return this.metadata?.name;
  }

  get stateDisplay() {
    return this.status?.phase || (this.status?.ready ? 'Ready' : '');
  }

  get _availableActions() {
    return [];
  }

  t(...args) {
    return ctx.t(...args);
  }

  currentRouter() {
    return ctx.router;
  }

  toJSON() {
    return { ...this };
  }

  replaceWith(data) {
    Object.keys(this).forEach((k) => {
      if (!(k in data)) {
        delete this[k];
      }
    });
    Object.assign(this, data);
  }

  async save() {
    const schema = this.schema;

    if (!schema) {
      throw new Error(`The server doesn't serve ${ this.type }`);
    }
    const body = JSON.parse(JSON.stringify(this));
    const ns = this.metadata?.namespace;
    const data = this.metadata?.resourceVersion ? await request('PUT', urlFor(schema, ns, this.metadata.name), body) : await request('POST', urlFor(schema, ns), body);

    this.replaceWith(data);
    ctx.store.forget(this.type);

    return this;
  }

  async remove() {
    await request('DELETE', urlFor(this.schema, this.metadata?.namespace, this.metadata?.name));
    ctx.store.forget(this.type);
  }
}
