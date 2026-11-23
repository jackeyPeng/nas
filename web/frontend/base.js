// 基座服务（M2「基座服务（一）」：PostgreSQL/Valkey/Mosquitto 纳管）前端逻辑，
// 独立于 app.js 按需维护，模式与 apps.js 完全一致：
//
// 通过 window.basePanel() 向 nasPanel() 的返回对象做一次 Object.assign 合并
// （app.js 里的挂钩见 `Object.assign({...}, window.basePanel ? window.basePanel() : {})`），
// 因此这里写的方法里 this 仍指向同一个 Alpine 组件实例，可以直接调用
// this.api()/this.showToast()/this.t() 等既有 helper，无需重新实现。
//
// 接口契约（web/modules/base/api.go）：
//   GET  /api/base                        -> { components: [componentSummary] }
//   POST /api/base/<component>/install    装完默认停，不自启
//   POST /api/base/<component>/uninstall
//   POST /api/base/<component>/enable     一键启用（enable + start）
//   POST /api/base/<component>/disable
// component 取值固定为 pgsql | cache | mqtt，均幂等、仅限管理员。

function basePanel() {
    return {
        // ─── 状态 ───
        baseComponents: [],
        baseLoading: false,
        baseActing: {}, // component -> bool，单个卡片按钮的 loading 态，不互相阻塞

        // ─── 列表 ───
        async loadBaseServices() {
            this.baseLoading = true;
            const data = await this.api('/base');
            if (data) this.baseComponents = data.components || [];
            this.baseLoading = false;
        },

        // ready 徽标：已安装且已启用才算真正就绪，与 base.Ready() 的判定口径一致
        baseReady(comp) {
            return !!(comp && comp.installed && comp.enabled);
        },

        baseHealthBadge(comp) {
            if (!comp || !comp.installed) return { text: this.t('base.not_installed'), style: 'background: var(--card-bg-alt);color: var(--text-light)' };
            if (comp.health === 'running') return { text: this.t('base.health_running'), style: 'background: var(--success-light);color: var(--success)' };
            if (comp.health === 'unhealthy') return { text: this.t('base.health_unhealthy'), style: 'background: var(--danger-light);color: var(--danger)' };
            return { text: this.t('base.health_stopped'), style: 'background: var(--card-bg-alt);color: var(--text-light)' };
        },

        // ─── 安装/启用/停用/卸载：均幂等，失败态由 this.api() 统一弹 toast ───
        async baseInstall(comp) {
            this.baseActing[comp.component] = true;
            try {
                const data = await this.api('/base/' + encodeURIComponent(comp.component) + '/install', { method: 'POST' });
                if (data) { this.showToast(data.message || this.t('base.install_done'), 'success'); await this.loadBaseServices(); }
            } finally {
                this.baseActing[comp.component] = false;
            }
        },

        async baseEnable(comp) {
            this.baseActing[comp.component] = true;
            try {
                const data = await this.api('/base/' + encodeURIComponent(comp.component) + '/enable', { method: 'POST' });
                if (data) { this.showToast(data.message || this.t('base.enable_done'), 'success'); await this.loadBaseServices(); }
            } finally {
                this.baseActing[comp.component] = false;
            }
        },

        async baseDisable(comp) {
            this.baseActing[comp.component] = true;
            try {
                const data = await this.api('/base/' + encodeURIComponent(comp.component) + '/disable', { method: 'POST' });
                if (data) { this.showToast(data.message || this.t('base.disable_done'), 'success'); await this.loadBaseServices(); }
            } finally {
                this.baseActing[comp.component] = false;
            }
        },

        async baseUninstall(comp) {
            this.baseActing[comp.component] = true;
            try {
                const data = await this.api('/base/' + encodeURIComponent(comp.component) + '/uninstall', { method: 'POST' });
                if (data) { this.showToast(data.message || this.t('base.uninstall_done'), 'success'); await this.loadBaseServices(); }
            } finally {
                this.baseActing[comp.component] = false;
            }
        },

        // ─── 应用安装确认步骤的就绪徽标（index.html 的 appsPkg.step === 'confirm' 区块调用） ───
        // pending.requires 是 manifest 声明的能力名数组（见 apps manifest），
        // 这里只按 pgsql/cache/mqtt 三种取对应组件状态展示，与应用申领的具体权限无关——
        // 只告知「这个基座现在是否就绪」，真正的放行判定仍在后端 CapabilitiesSupported。
        baseComponentByKey(key) {
            const map = { pgsql: 'pgsql', db: 'pgsql', cache: 'cache', mqtt: 'mqtt' };
            const component = map[key];
            return this.baseComponents.find(c => c.component === component) || null;
        },
    };
}

window.basePanel = basePanel;
