// 应用中心（M1-5，APP-SPEC §3.3/§4）前端逻辑，独立于 app.js 按需维护
// （架构约束：面板单页不膨胀，apps 相关状态/方法集中在这一个文件）。
//
// 通过 window.appsPanel() 向 nasPanel() 的返回对象做一次 Object.assign 合并
// （app.js 里的挂钩见 `Object.assign({...}, window.appsPanel ? window.appsPanel() : {})`），
// 因此这里写的方法里 this 仍指向同一个 Alpine 组件实例，可以直接调用
// this.api()/this.showToast()/this.t() 等既有 helper，无需重新实现。
//
// 健康轮询（10s，仅详情抽屉打开时）单独起一个 timer，与 app.js 里其它页面
// 的轮询（monitor/rclone 任务进度等）同一套停启模式：navigate() 离开页面
// 或关闭详情抽屉时清掉。

function appsPanel() {
    return {
        // ─── 状态 ───
        appsList: [],
        appsLoading: false,
        appsHealthMap: {}, // id -> { status, consecutiveFail, lastProbeAt, lastError }

        // 安装/升级共用同一个上传->确认两步对话框（mode 区分调用的接口）
        appsPkg: {
            show: false,
            mode: 'install', // install | upgrade
            step: 'upload', // upload | confirm
            packageFile: null,
            sigFile: null,
            submitting: false,
            pending: null, // install/upgrade 第一步返回的 pendingInstallResponse
        },

        appsDetail: {
            show: false,
            id: '',
            data: null,
            acting: false,
            delete: { show: false, keepData: false, confirmText: '' },
            healthTimer: null,
        },

        // ─── 列表 ───
        async loadApps() {
            this.appsLoading = true;
            const data = await this.api('/apps');
            if (data) {
                this.appsList = data.apps || [];
                this.appsList.forEach(a => this.refreshAppHealth(a.id));
            }
            this.appsLoading = false;
        },

        async refreshAppHealth(id) {
            const data = await this.api('/apps/' + encodeURIComponent(id) + '/health');
            if (data) this.appsHealthMap[id] = data;
        },

        // running -> 绿 / unhealthy -> 红 / stopped（含未知） -> 灰
        appHealthBadge(id) {
            const st = (this.appsHealthMap[id] || {}).status;
            if (st === 'running') return { text: this.t('apps.health_running'), style: 'background: var(--success-light);color: var(--success)' };
            if (st === 'unhealthy') return { text: this.t('apps.health_unhealthy'), style: 'background: var(--danger-light);color: var(--danger)' };
            return { text: this.t('apps.health_stopped'), style: 'background: var(--card-bg-alt);color: var(--text-light)' };
        },

        openApp(app) {
            window.open('/apps/' + encodeURIComponent(app.id) + (app.entry || '/'), '_blank');
        },

        // ─── 文件 -> base64（签名文件是原始二进制，需 base64 后塞进 multipart 字段） ───
        readFileAsBase64(file) {
            return new Promise((resolve, reject) => {
                const reader = new FileReader();
                reader.onload = () => resolve(btoa(new Uint8Array(reader.result).reduce((s, b) => s + String.fromCharCode(b), '')));
                reader.onerror = () => reject(reader.error);
                reader.readAsArrayBuffer(file);
            });
        },

        // ─── 安装/升级共用：Step 1 上传包（升级无需确认权限，装完即生效） ───
        openInstallDialog() {
            this.appsPkg = { show: true, mode: 'install', step: 'upload', packageFile: null, sigFile: null, submitting: false, pending: null };
        },
        openUpgradeDialog() {
            this.appsPkg = { show: true, mode: 'upgrade', step: 'upload', packageFile: null, sigFile: null, submitting: false, pending: null };
        },

        onPkgPicked(e) { this.appsPkg.packageFile = e.target.files[0] || null; },
        onSigPicked(e) { this.appsPkg.sigFile = e.target.files[0] || null; },

        async submitPkg() {
            const pk = this.appsPkg;
            if (!pk.packageFile || !pk.sigFile) {
                this.showToast(this.t('apps.err_need_package_and_sig'), 'error');
                return;
            }
            pk.submitting = true;
            try {
                const sigB64 = await this.readFileAsBase64(pk.sigFile);
                const form = new FormData();
                form.append('package', pk.packageFile);
                form.append('signature', sigB64);
                if (pk.mode === 'install') {
                    const data = await this.api('/apps/install', { method: 'POST', body: form });
                    if (data) { pk.pending = data; pk.step = 'confirm'; }
                } else {
                    const data = await this.api('/apps/' + encodeURIComponent(this.appsDetail.id) + '/upgrade', { method: 'POST', body: form });
                    if (data) {
                        this.appsDetail.data = data;
                        this.showToast(this.t('apps.upgrade_done'), 'success');
                        pk.show = false;
                        this.loadApps();
                    }
                }
            } finally {
                pk.submitting = false;
            }
        },

        async confirmInstall() {
            const pk = this.appsPkg;
            if (!pk.pending) return;
            pk.submitting = true;
            try {
                const data = await this.api('/apps/install/' + encodeURIComponent(pk.pending.id) + '/confirm', { method: 'POST' });
                if (data) {
                    this.showToast(this.t('apps.install_done'), 'success');
                    pk.show = false;
                    await this.loadApps();
                }
            } finally {
                pk.submitting = false;
            }
        },

        cancelPkgDialog() { this.appsPkg.show = false; },

        // ─── 详情抽屉 ───
        async openAppDetail(app) {
            this.appsDetail.id = app.id;
            this.appsDetail.data = null;
            this.appsDetail.show = true;
            this.appsDetail.delete = { show: false, keepData: false, confirmText: '' };
            await this.loadAppDetail();
            this.startAppDetailHealthPolling();
        },

        closeAppDetail() {
            this.appsDetail.show = false;
            this.stopAppDetailHealthPolling();
        },

        async loadAppDetail() {
            const data = await this.api('/apps/' + encodeURIComponent(this.appsDetail.id));
            if (data) this.appsDetail.data = data;
        },

        startAppDetailHealthPolling() {
            this.stopAppDetailHealthPolling();
            const id = this.appsDetail.id;
            this.appsDetail.healthTimer = setInterval(() => {
                if (!this.appsDetail.show) { this.stopAppDetailHealthPolling(); return; }
                this.refreshAppHealth(id);
            }, 10000);
            this.refreshAppHealth(id);
        },

        stopAppDetailHealthPolling() {
            if (this.appsDetail.healthTimer) { clearInterval(this.appsDetail.healthTimer); this.appsDetail.healthTimer = null; }
        },

        // ─── 启停/重启 ───
        async appAction(action) {
            const id = this.appsDetail.id;
            this.appsDetail.acting = true;
            try {
                const data = await this.api('/apps/' + encodeURIComponent(id) + '/' + action, { method: 'POST' });
                if (data) {
                    this.appsDetail.data = data;
                    this.showToast(this.t('apps.action_done_' + action), 'success');
                    this.refreshAppHealth(id);
                    this.loadApps();
                }
            } finally {
                this.appsDetail.acting = false;
            }
        },

        // ─── 卸载：保留数据 checkbox + 二次确认 ───
        openDeleteDialog() {
            this.appsDetail.delete = { show: true, keepData: false, confirmText: '' };
        },

        async confirmDeleteApp() {
            const del = this.appsDetail.delete;
            if (del.confirmText !== this.appsDetail.id) {
                this.showToast(this.t('apps.err_confirm_text_mismatch'), 'error');
                return;
            }
            const id = this.appsDetail.id;
            const data = await this.api('/apps/' + encodeURIComponent(id), {
                method: 'DELETE',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ keepData: del.keepData }),
            });
            if (data) {
                this.showToast(this.t('apps.delete_done'), 'success');
                this.appsDetail.delete.show = false;
                this.closeAppDetail();
                this.loadApps();
            }
        },

        cancelDeleteApp() { this.appsDetail.delete.show = false; },
    };
}

window.appsPanel = appsPanel;
