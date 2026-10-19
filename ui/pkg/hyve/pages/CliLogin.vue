<script>
import { Banner } from '@components/Banner';
import { MANAGEMENT } from '@shell/config/types';
import { SETTING } from '@shell/config/settings';
import {
  CliLoginError, callbackURL, clearStash, encryptToken, importPublicKey, parseRequest, sessionTTLMillis
} from '../utils/cli-login';

// The hyve CLI's sign-in page; see utils/cli-login.ts for the flow.
export default {
  name:       'HyveCliLogin',
  components: { Banner },

  data() {
    return {
      phase:     'loading', // loading | ask | working | done | cancelled | invalid
      error:     '',
      request:   null,
      key:       null,
      user:      '',
      ttlMillis: sessionTTLMillis(null),
    };
  },

  async mounted() {
    // Reached the page, so the copy kept across Rancher's sign-in is spent.
    clearStash();

    try {
      this.request = parseRequest(this.$route.query);
      this.key = await importPublicKey(this.request.publicKey);
    } catch (e) {
      this.phase = 'invalid';
      this.error = e instanceof CliLoginError ? e.message : `${ e }`;

      return;
    }

    const [me, ttl] = await Promise.allSettled([
      this.$store.dispatch('rancher/request', { url: '/v3/users?me=true' }),
      this.$store.dispatch('management/find', { type: MANAGEMENT.SETTING, id: SETTING.AUTH_USER_SESSION_TTL_MINUTES }),
    ]);

    if (me.status === 'fulfilled') {
      const u = me.value?.data?.[0] || {};

      this.user = u.username || u.name || u.id || '';
    }
    if (ttl.status === 'fulfilled') {
      this.ttlMillis = sessionTTLMillis(ttl.value?.value || ttl.value?.default);
    }
    this.phase = 'ask';
  },

  computed: {
    duration() {
      const hours = Math.round(this.ttlMillis / 3600000);

      return hours >= 1 ? `${ hours } hour${ hours === 1 ? '' : 's' }` : `${ Math.round(this.ttlMillis / 60000) } minutes`;
    },
  },

  methods: {
    async allow() {
      this.phase = 'working';
      this.error = '';
      try {
        // As Rancher's Account & API Keys page does. Rancher caps the TTL at
        // auth-token-max-ttl-minutes.
        const res = await this.$store.dispatch('rancher/request', {
          url:    '/v3/tokens',
          method: 'post',
          data:   {
            type:        'token',
            description: `hyve CLI sign-in (${ new Date().toISOString() })`,
            ttl:         this.ttlMillis,
          },
        });
        const token = res?.token;

        if (!token) {
          throw new Error('Rancher didn\'t return an API key.');
        }
        const encrypted = await encryptToken(this.key, token);
        const expiresAt = res.expiresAt || await this.expiryOf(res.id);

        this.phase = 'done';
        window.location.assign(callbackURL(this.request, { token: encrypted, expiresAt }));
      } catch (e) {
        this.phase = 'ask';
        this.error = e?.message || e?._statusText || `Creating the API key failed: ${ JSON.stringify(e) }`;
      }
    },

    // The create response has no expiry (Rancher 2.14), so read it back,
    // as the API Keys page does. An unknown expiry is sent as none; the CLI
    // then finds out when Rancher stops accepting the key.
    async expiryOf(id) {
      try {
        const t = await this.$store.dispatch('rancher/request', { url: `/v3/tokens/${ encodeURIComponent(id) }` });

        return t?.expiresAt || '';
      } catch (e) {
        return '';
      }
    },

    cancel() {
      this.phase = 'cancelled';
      window.location.assign(callbackURL(this.request, { error: 'cancelled' }));
    },
  },
};
</script>

<template>
  <div class="cli-login">
    <h1>Sign in to the hyve CLI</h1>

    <div v-if="phase === 'loading'">
      <i class="icon icon-spinner icon-spin" />
    </div>

    <template v-else-if="phase === 'invalid'">
      <Banner
        color="error"
        :label="error"
      />
      <p>Run <code>hyve login</code> again, and open the link it prints on this machine.</p>
    </template>

    <template v-else-if="phase === 'done'">
      <p>Signed in. You can close this tab and return to the terminal.</p>
    </template>

    <template v-else-if="phase === 'cancelled'">
      <p>Cancelled. You can close this tab.</p>
    </template>

    <template v-else>
      <p>
        A hyve CLI on this computer is asking to sign in to Rancher
        <template v-if="user">
          as <strong>{{ user }}</strong>
        </template>.
        It gets an API key that can do anything you can, for {{ duration }}
        or until you delete it under Account &amp; API Keys.
      </p>
      <p>Only allow this if you just ran <code>hyve login</code> on this computer.</p>

      <Banner
        v-if="error"
        color="error"
        :label="error"
      />

      <div class="buttons">
        <button
          class="btn role-secondary"
          :disabled="phase === 'working'"
          @click="cancel"
        >
          Cancel
        </button>
        <button
          class="btn role-primary"
          :disabled="phase === 'working'"
          @click="allow"
        >
          <i
            v-if="phase === 'working'"
            class="icon icon-spinner icon-spin"
          />
          Allow
        </button>
      </div>
    </template>
  </div>
</template>

<style lang="scss" scoped>
.cli-login {
  max-width: 560px;
  margin: 60px auto;
  padding: 0 20px;

  p {
    margin: 15px 0;
  }

  .buttons {
    display: flex;
    gap: 10px;
    justify-content: flex-end;
    margin-top: 30px;
  }
}
</style>
