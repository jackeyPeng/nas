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

        // 应用市场（M2（二），APP-SPEC §7）：index.json 一键安装，安装链路
        // 复用 appsPkg 同一套 confirm 对话框（market/install 返回体与
        // /apps/install 完全同形，即 pendingInstallResponse）
        marketApps: [],
        marketLoading: false,
        marketStale: false,
        marketInstalling: '', // 正在安装的 appId，用于按钮 disabled

        // 外部应用登记（APP-SPEC §10，#37）：机器上已自行部署的服务，登记
        // 进应用中心统一列表，不走安装/升级/卸载那套签名包流程。
        externalReg: {
            show: false,
            submitting: false,
            form: { id: '', name: '', url: '', unit: '', healthzUrl: '', icon: '' },
        },
        externalUnregister: {
            show: false,
            id: '',
            confirmText: '',
        },
        externalActing: {}, // id -> bool，按钮 disabled 用

        // ─── 应用市场 ───
        async loadMarket() {
            this.marketLoading = true;
            const data = await this.api('/apps/market');
            if (data) {
                this.marketApps = data.apps || [];
                this.marketStale = !!data.stale;
            }
            this.marketLoading = false;
        },

        async installFromMarket(app) {
            if (!app.installable || this.marketInstalling) return;
            this.marketInstalling = app.id;
            try {
                const data = await this.api('/apps/market/install', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ id: app.id, version: app.latest }),
                });
                if (data) {
                    this.appsPkg = { show: true, mode: 'install', step: 'confirm', packageFile: null, sigFile: null, submitting: false, pending: data };
                    this.loadBaseServices(); // 确认步骤要展示 db/cache/mqtt 就绪徽标
                }
            } finally {
                this.marketInstalling = '';
            }
        },

        // ─── 列表（统一列表：托管应用 kind:"managed" + 外部应用 kind:"external"，
        //      §10.3。外部应用的 status 已经在列表响应里，不走 /health 轮询
        //      接口——那是托管应用专用的） ───
        async loadApps() {
            this.appsLoading = true;
            const data = await this.api('/apps');
            if (data) {
                this.appsList = data.apps || [];
                this.appsList.filter(a => a.kind !== 'external').forEach(a => this.refreshAppHealth(a.id));
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

        // 「打开」改走一次性票据跳转入口（M1-7）：window.open 新开页面不带
        // Authorization 头，直开 /apps/<id>/... 会话校验必 401；改成先打开
        // /apps/<id>/open?entry=<entry>，由面板校验当前会话后 302 带票据
        // 落回真实应用页面，浏览器原生导航自动带票据 query，不需要 JS 设头。
        // entry 拼接保持原逻辑：manifest.entry 是相对 /apps/<id> 的路径
        // （以 / 开头，如 "/" 或 "/gallery"），不是完整路径。
        openApp(app) {
            const entry = '/apps/' + app.id + (app.entry || '/');
            // window.open 直开 /open 仍不带 Authorization 头（浏览器导航无自定义
            // header）——先在面板页内 fetch（带 token，redirect:follow 消费 302
            // 拿到已含票据的最终 URL），再 window.open 那个 URL；票据一次性，
            // 新窗口首次加载即消费，浏览器导航无需任何 header。
            const tok = localStorage.getItem('nas_token') || '';
            const headers = tok ? { 'Authorization': 'Bearer ' + tok } : {};
            fetch('/apps/' + encodeURIComponent(app.id) + '/open?entry=' + encodeURIComponent(entry), { headers, redirect: 'follow' })
                .then(res => { if (res.ok) window.open(res.url, '_blank'); else alert(this.t('apps.open_failed') || '打开失败'); })
                .catch(() => alert(this.t('apps.open_failed') || '打开失败'));
        },

        // 外部应用「打开」：直连登记时填的 url，新标签页打开，不走票据/反代
        // 流程（§10.1——核心侧不碰其鉴权，面板只是个门户卡）。
        openExternalApp(app) {
            window.open(app.url, '_blank');
        },

        // unknown/running -> 灰/绿，unhealthy -> 红，stopped -> 灰
        externalStatusBadge(app) {
            if (app.status === 'running') return { text: this.t('apps.health_running'), style: 'background: var(--success-light);color: var(--success)' };
            if (app.status === 'unhealthy') return { text: this.t('apps.health_unhealthy'), style: 'background: var(--danger-light);color: var(--danger)' };
            return { text: this.t('apps.health_stopped'), style: 'background: var(--card-bg-alt);color: var(--text-light)' };
        },

        // ─── 外部应用：登记 ───
        openExternalRegisterDialog() {
            this.externalReg = {
                show: true,
                submitting: false,
                form: { id: '', name: '', url: '', unit: '', healthzUrl: '', icon: '' },
            };
        },

        cancelExternalRegister() { this.externalReg.show = false; },

        async submitExternalRegister() {
            const f = this.externalReg.form;
            if (!f.id || !f.name || !f.url) {
                this.showToast(this.t('apps.err_external_required'), 'error');
                return;
            }
            this.externalReg.submitting = true;
            try {
                const data = await this.api('/apps/external', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(f),
                });
                if (data) {
                    this.showToast(this.t('apps.external_register_done'), 'success');
                    this.externalReg.show = false;
                    this.loadApps();
                }
            } finally {
                this.externalReg.submitting = false;
            }
        },

        // ─── 外部应用：启停/重启（仅登记了 unit 时可用） ───
        async externalAction(app, action) {
            if (!app.unit) return;
            this.externalActing[app.id] = true;
            try {
                const data = await this.api('/apps/external/' + encodeURIComponent(app.id) + '/' + action, { method: 'POST' });
                if (data) {
                    this.showToast(this.t('apps.action_done_' + action), 'success');
                    this.loadApps();
                }
            } finally {
                this.externalActing[app.id] = false;
            }
        },

        // ─── 外部应用：注销登记（仅删登记行，不动服务/数据，§10.1） ───
        openExternalUnregisterDialog(app) {
            this.externalUnregister = { show: true, id: app.id, confirmText: '' };
        },

        cancelExternalUnregister() { this.externalUnregister.show = false; },

        async confirmExternalUnregister() {
            const u = this.externalUnregister;
            if (u.confirmText !== u.id) {
                this.showToast(this.t('apps.err_external_id_mismatch'), 'error');
                return;
            }
            const data = await this.api('/apps/external/' + encodeURIComponent(u.id), {
                method: 'DELETE',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ confirm: true }),
            });
            if (data) {
                this.showToast(this.t('apps.external_unregister_done'), 'success');
                this.externalUnregister.show = false;
                this.loadApps();
            }
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
            this.loadBaseServices(); // 确认步骤要展示 db/cache/mqtt 就绪徽标，提前拉一次基座状态
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
