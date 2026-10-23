function nasPanel() {
    return {
        token: localStorage.getItem('nas_token') || '',
        page: 'dashboard',
        navGroup: 'overview',
        loading: false,
        loginError: '',
        loginForm: { username: '', password: '', totp: '' },
        dashboard: {},
        dashboardLoaded: false,
        services: [],
        installingServices: false,
        installMsg: '',
        users: [],
        storage: {},
        smartStatus: '',
        firewallStatus: '',
        firewall: { loaded: false, installed: false, active: false, default_incoming: '', rules: [] },
        firewallPresets: [
            { label: 'SSH (22)', port: '22', proto: 'tcp' },
            { label: 'Samba (445)', port: '445', proto: 'tcp' },
            { label: 'Samba (139)', port: '139', proto: 'tcp' },
            { label: 'FTP (21)', port: '21', proto: 'tcp' },
            { labelKey: 'msg.ftp_passive', port: '30000:31000', proto: 'tcp' },
            { label: 'NFS (2049)', port: '2049', proto: 'any' },
            { label: 'WebDAV (8080)', port: '8080', proto: 'tcp' },
            { labelKey: 'msg.panel_port', port: '8090', proto: 'tcp' },
        ],
        firewallForm: { port: '', proto: 'tcp', action: 'allow', from: '', comment: '' },
        firewallSaving: false,
        monitor: {},
        connections: [],
        recycleItems: [],
        recycleShares: {},
        recycleShareFilter: '',
        monitorTimer: null,
        monitorShowDetail: false,
        monitorShowAdvanced: false,
        alertConfig: {},
        health: { overall: 'ok', checks: [] },
        healthLoaded: false,
        // Config module
        envConfig: {},
        sambaShares: [],
        vsftpdUsers: [],
        enabledServices: '',
        showAddShare: false,
        shareForm: { name: '', path: '', comment: '', valid_users: '', read_only: false },
        ftpUserForm: { username: '' },
        configFileForm: { name: '', content: '' },
        svcToggleForm: { service: '' },
        // Disk management
        diskInfo: '',
        diskMounts: '',
        diskLVM: '',
        diskIOStat: '',
        diskSmartDetail: '',
        diskPartitions: '',
        diskMkdirForm: { path: '' },
        mountForm: { device: '', mountpoint: '', fstype: '' },
        unmountForm: { target: '' },
        formatForm: { device: '', fstype: 'ext4' },
        // Disk management - new
        diskStatus: [],
        diskFstab: '',
        quickSetupForm: { device: '', mountpoint: '', fstype: 'ext4', samba: false },
        pool: {},
        showCreatePool: false,
        showExtendPool: false,
        showQuickSetup: false,
        showManual: false,
        poolCreateForm: { devices: '', vg_name: 'vg_nas', lv_name: 'data', mountpoint: '/data', fstype: 'ext4' },
        poolExtendForm: { device: '', vg_name: 'vg_nas' },
        // Wizard
        wizard: {},
        wizardStatus: {},
        wizardMode: '',
        wizardGoal: '',
        wizardGoalText: '',
        wizardLoading: false,
        wizMode: '',
        wizGoal: '',
        wizDisks: [],
        wizStep: 1,
        allDisks: [],
        progressSteps: [],
        progressShow: false,
        progressTitle: '',
        // Storage overview
        storageOverview: {},
        diskmgmtTab: 'overview',
        showDiskDetail: false,
        selectedDisk: null,
        selectedDiskDevice: '',
        operationsLog: [],
        // 文件管理器(只读 v1)
        filesPage: {
            path: '', entries: [], breadcrumb: [], truncated: false, loading: false,
            usageText: '', selected: [], uploadProgress: { active: false, percent: 0, loaded: 0, total: 0 },
            showMkdir: false, mkdirName: '', mkdirError: '',
            showRename: false, renameEntry: null, renameName: '', renameError: '',
            showTransfer: false, transferMode: '', transferEntries: [], transferPath: '', transferBreadcrumb: [], transferPickerEntries: [],
            showDelete: false, deleteEntries: [], deleteConfirmName: '', deleteRequiredName: '', deleteError: '',
            preview: { open: false, kind: '', name: '', content: '', url: '' },
            // 权限对话框(单条属性 + 批量改权限共用,batchMode 区分)
            permsDialog: {
                show: false, entry: null, stat: null, modeInput: '', recursive: false,
                ownerSel: '', groupSel: '', origOwner: '', origGroup: '',
                confirmText: '', error: '', saving: false,
                batchMode: false, batchEntries: [], batchProgress: { active: false, done: 0, total: 0 },
            },
            relJoin(name) { return this.path ? this.path + '/' + name : name; },
        },
        // Shared folders
        sharedFolders: [],
        showAddFolder: false,
        showUserDropdown: false,
        showAccessFor: '',
        configIssues: { has_issues: false, issues: [] },
        pendingCount: 0,
        operationLogs: [],
        auditLogs: [],
        auditLogTotal: 0,
        auditLogPage: 0,
        auditLogFilter: { action: '', days: 7 },

        // ═══ 事件中心 ═══
        eventList: [],
        eventTotal: 0,
        eventPage: 0,
        eventFilter: { source: '', type: '', days: 7 },
        eventStats: { info: 0, success: 0, warn: 0, error: 0, total: 0 },
        recentAlerts: [],

        async loadRecentAlerts() {
            const data = await this.api('/events?limit=5&days=7&type=error');
            const warns = await this.api('/events?limit=5&days=7&type=warn');
            const all = [...(data && data.events ? data.events : []),
                         ...(warns && warns.events ? warns.events : [])];
            all.sort((a, b) => (b.time || '').localeCompare(a.time || ''));
            this.recentAlerts = all.slice(0, 5);
        },

        async loadEvents() {
            const params = new URLSearchParams({
                limit: '50',
                offset: String(this.eventPage * 50),
                days: String(this.eventFilter.days || 7)
            });
            if (this.eventFilter.source) params.set('source', this.eventFilter.source);
            if (this.eventFilter.type) params.set('type', this.eventFilter.type);
            const data = await this.api('/events?' + params.toString());
            if (data) {
                this.eventList = data.events || [];
                this.eventTotal = data.total || 0;
            }
        },

        async loadEventStats() {
            const data = await this.api('/events/stats?days=' + (this.eventFilter.days || 7));
            if (data) this.eventStats = data;
        },

        // 事件文案:优先 events.<code> i18n 键（支持命名占位符插值），缺失时兜底 code 本身
        eventText(ev) {
            window.Alpine.store('i18n').lang; // reactive dependency
            const params = this.eventParams(ev);
            if (window.hasKey && window.hasKey('events.' + ev.code)) {
                return window.t('events.' + ev.code, params);
            }
            return ev.code;
        },

        // 解析事件 params JSON 为对象（坏数据兜底空对象）
        eventParams(ev) {
            try {
                const p = ev.params ? JSON.parse(ev.params) : {};
                return (p && typeof p === 'object') ? p : {};
            } catch (e) { return {}; }
        },

        eventTypeStyle(type) {
            const m = {
                error:   'background:var(--danger-light);color:var(--danger)',
                warn:    'background:var(--warn-light);color:var(--warn)',
                success: 'background:var(--success-light);color:var(--success)',
                info:    'background:var(--info-light);color:var(--info)'
            };
            return m[type] || m.info;
        },

        eventTypeText(type) {
            window.Alpine.store('i18n').lang; // reactive dependency
            return window.t('events.type_' + type) || type;
        },

        fmtEventTime(ts) {
            if (!ts) return '';
            return ts.replace('T', ' ').substring(0, 16);
        },

        folderForm: { pool: '', name: '', permission: 'readwrite', valid_users: [], recycle_bin: false, nfs: false, quota_gb: 0 },
        showFolderPerm: false,
        folderPermForm: { name: '', path: '', pool: '', permission: 'readwrite', valid_users: '', recycle_bin: false },
        // Pool extend
        showExtendPool: false,
        showReplaceDisk: false,
        replaceDiskForm: { md_device: '', old_device: '', new_device: '', pool_name: '' },
        // RAID expand
        showRAIDExpand: false,
        raidExpandForm: { mdDevice: '', device: '', poolName: '', raidType: '' },
        // Backup
        backups: [],
        backupLoading: false,
        dataBackupTarget: '',
        dataBackupLoading: false,
        // 凭证保险箱 v2（零知识：加解密全部在浏览器端，服务器只存密文）
        vaultState: 'uninitialized', // uninitialized | locked | unlocked
        vaultLoading: false,
        vaultItemCount: 0,
        vaultLegacyCount: 0,
        vaultMasterKey: null,       // Uint8Array，仅内存持有，绝不持久化
        vaultItems: [],             // 解锁后的明文条目缓存（内存）: {id,name,category,url,username,notes_preview,secret,notes,updated_at}
        vaultSetupForm: { passphrase: '', confirm: '' },
        vaultUnlockForm: { passphrase: '' },
        vaultUnlockError: '',
        vaultLockedRemaining: 0,
        vaultLockedTimer: null,
        vaultAttemptsLeft: null,
        showVaultForm: false,
        vaultForm: { id: null, name: '', category: '', url: '', username: '', secret: '', notes: '' },
        vaultRevealId: null,        // 当前处于"已显示明文"状态的条目 id（15 秒后自动遮罩）
        vaultRevealTimer: null,
        vaultIdleTimer: null,
        showChangePassphrase: false,
        changePassphraseForm: { oldPassphrase: '', newPassphrase: '', confirm: '' },
        changePassphraseLoading: false,
        vaultLegacyItems: [],
        showLegacyMigration: false,
        legacyMigrating: false,
        // 密码生成器（独立页面，不依赖保险箱解锁）
        pwgen: {
            length: 16,
            upper: true,
            lower: true,
            digits: true,
            symbols: true,
            value: '',
            strength: 0,
        },
        pwgenHistory: [],
        // Panel update
        panelUpdate: { current: '', latest: '', has_update: false, error: '', detail: '' },
        panelUpdating: false,
        // Rclone
        rcloneStatus: {},
        rcloneRemotes: [],
        rcloneTasks: [],
        sharedDirs: [],
        rcloneLogs: [],
        rcloneProgressMap: {},
        rcloneProgressTimer: null,
        rcloneWasRunning: [],
        showAddRemote: false,
        editingRemote: '',   // 非空 = 编辑模式
        showAddTask: false,
        editingTask: null,   // 非空 = 编辑模式（任务对象）
        showTaskAdvanced: false,
        remoteForm: { name: '', type: '', provider: 'AWS', endpoint: '', access_key_id: '', secret_access_key: '', region: '', host: '', port: '22', user: '', pass: '', url: '', vendor: 'nextcloud', local_path: '' },
        remoteCreating: false,
        remoteTesting: '',
        taskForm: { name: '', direction: 'upload', source: '', sub_path: '', remote: '', dest_path: '', mode: 'sync', schedule: '', bandwidth: 0, transfers: 4 },
        taskCreating: false,
        // System settings
        // System settings (new unified)
        // 组件版本（系统详情页）
        components: { items: [], panel: null },
        noticeSections: [],
        componentCategories: ['文件共享', '对象存储', '网页管理', '系统防护', '存储管理', '运行环境'],
        // 硬件档案（系统详情页）
        hardware: null,

        sysSettings: {
            hostname: '',
            network: {},
            time: {},
            timezone: 'Asia/Shanghai',
            ssh: {},
            sysctl: [],
            updates: {},
            services: []
        },
        // HTTPS certificate
        httpsStatus: { type: 'none', services: [] },
        httpsDomain: '',
        httpsCustomDomain: '',
        httpsCustomCert: '',
        httpsCustomKey: '',
        lang: window.currentLang || 'zh-CN',
        // Diagnostics
        diagItems: [],
        diagHistory: [],
        diagConfig: { time_window_start: '02:00', time_window_end: '06:00', temp_limit: 55, io_limit: 70, scrub_speed_max: 100000 },
        mobileMenu: false,
        resetMsg: '',
        logsModal: false,
        logsService: '',
        logsContent: '',
        addUserModal: false,
        addUserForm: { username: '', password: '' },
        pwdModal: false,
        pwdUser: '',
        pwdForm: { password: '' },
        mustChangePassword: false,
        needTotp: false,
        twoFA: { enabled: false, pending: false, secret: '', otpauth_url: '' },
        twoFACode: '',
        importablePools: [],
        // Users module - new
        userTab: 'list', // list | groups | matrix | logs
        userGroups: [],
        groupForm: { name: '', comment: '', members: '' },
        showGroupModal: false,
        // 修复 home 权限对话框(type-to-confirm 输入用户名)
        fixHomeDialog: { show: false, username: '', confirmText: '', saving: false },
        permMatrix: { folders: [], users: [], matrix: {} },
        loginLogs: [],
        loginLogsLoading: false,
        // User wizard
        userWizardStep: 1,
        userWizardForm: {
            username: '', password: '', password2: '',
            svc_samba: true, svc_ftp: true, svc_webdav: true,
            quota_gb: 0,
            sharePerms: {} // folder -> perm
        },
        userWizardShares: [],
        toast: { show: false, msg: '', type: 'success' },

        // AI 助手
        aiStatus: { enabled: false, configured: false, model: '', preset: '' },
        aiTab: 'chat',
        aiConfigForm: { preset: 'deepseek', base_url: '', api_key: '', model: '' },
        aiTesting: false,
        aiConversations: [],
        aiConversationId: '',
        aiMessages: [],
        aiInput: '',
        aiThinking: false,
        // 写操作确认门：pendingAction 来自某条 assistant 消息的 pendingAction 字段
        // （见 handleAISSEEvent），confirmWordInput 是用户在弹窗里敲的确认词，
        // genPassword 是 create_user 专用的一次性生成密码（只在本次会话内存里，
        // 确认成功/取消/弹窗关闭都要清空，绝不进 aiMessages/任何持久化状态）。
        aiConfirm: { show: false, action: null, confirmWordInput: '', genPassword: '', submitting: false },
        // 任务引擎：任务 tab 的任务列表 + 进度轮询句柄（running/paused 存在时 2s 一轮）
        taskJobs: [],
        taskJobsTimer: null,

        // ═══ 通知中心（铃铛 + 建议卡 + 周报，F2 Task 6）═══
        // notifPanel：铃铛展开态 + 未读数 + 面板三段数据（建议卡/周报/通知列表）。
        // AI 关断时整个通知中心隐藏（notificationsVisible getter），轮询 30s 一轮，
        // document.hidden 时暂停、navigate 离开/卸载时清定时器（对照 taskJobsTimer 模式）。
        notifPanel: { open: false, unreadCount: 0, notifications: [], suggestions: [], report: null, loading: false },
        notifPollTimer: null,
        // notifConfirm：接受建议的确认弹窗独立状态对象，不与 aiConfirm 共用实例
        // （避免聊天确认门与通知中心确认门互踩）。字段形状与 aiConfirm 对齐，
        // action 存 accept 端点返回的 {action_id, confirm_word, summary, job_id}。
        notifConfirm: { show: false, action: null, confirmWordInput: '', submitting: false },

        init() {
            if (this.token) {
                this.navigate('dashboard');
            }
            // AI 助手入口显隐取决于 enabled：应用启动时拉一次状态（未登录时 token 为空，
            // api() 会带上空 Authorization，后端 AuthMiddleware 会拒绝——这里跟随登录态，
            // 登录后 navigate('dashboard') 不会主动加载 ai 状态，所以在 init 里单独拉一次）。
            if (this.token) {
                this.loadAIStatus().then(() => this.syncNotifPolling());
            }
            // Listen for i18n changes
            window.addEventListener('i18n:changed', (e) => {
                this.lang = e.detail.lang;
            });
            // 保险箱已解锁时，用户活动重置 15 分钟空闲自动锁定计时器
            ['click', 'keydown', 'mousemove'].forEach(evt => {
                window.addEventListener(evt, () => {
                    if (this.page === 'vault' && this.vaultState === 'unlocked') this.resetVaultIdleTimer();
                }, { passive: true });
            });
            // document.hidden 时暂停铃铛轮询，恢复可见时立即补一次并重新校准定时器
            // （对照任务 tab 轮询清理模式，这里额外加 visibility 这一档）。
            document.addEventListener('visibilitychange', () => {
                if (document.hidden) {
                    this.stopNotifPolling();
                } else {
                    this.syncNotifPolling();
                }
            });
            window.addEventListener('beforeunload', () => {
                if (this.vaultState === 'unlocked') this.vaultLock();
                this.stopTaskJobsPolling();
                this.stopNotifPolling();
            });
        },

        async switchLang(lang) {
            const ok = await window.switchLang(lang);
            if (ok) {
                this.lang = lang;
            }
        },

        // 本地化格式化运行时长（读取 store.lang 建立响应式依赖）
        fmtUptime(seconds) {
            window.Alpine.store('i18n').lang; // reactive dependency
            const s = Math.floor(seconds || 0);
            const d = Math.floor(s / 86400);
            const h = Math.floor((s % 86400) / 3600);
            const m = Math.floor((s % 3600) / 60);
            const t = window.t;
            if (d > 0) return t('common.uptime_fmt', [d, h, m]);
            if (h > 0) return t('common.uptime_fmt_short', [h, m]);
            return t('common.uptime_fmt_min', [m]);
        },

        // 本地化格式化 CPU 核数（读取 store.lang 建立响应式依赖）
        fmtCores(cores) {
            window.Alpine.store('i18n').lang; // reactive dependency
            return window.t('dashboard.cores_fmt', [cores]);
        },

        // 运行时文案翻译（confirm/toast 用，非响应式场景）
        t(key, params) {
            return window.t(key, params);
        },

        // 服务显示名翻译（后端 DisplayName 仅 S3 是中文）
        svcDisplay(name, fallback) {
            const map = { 'rclone-s3': 'services.label_s3' };
            return map[name] ? window.t(map[name]) : fallback;
        },
        // 服务描述翻译（后端 Description 全部是中文）
        svcDesc(name, fallback) {
            const map = {
                'smbd': 'services.desc_smbd',
                'nmbd': 'services.desc_nmbd',
                'nfs-kernel-server': 'services.desc_nfs',
                'vsftpd': 'services.desc_vsftpd',
                'rclone-webdav': 'services.desc_webdav',
                'rclone-s3': 'services.desc_s3',
                'fail2ban': 'services.desc_fail2ban',
            };
            return map[name] ? window.t(map[name]) : fallback;
        },

        // 组件分类名翻译（后端返回中文分类，仅用于显示）
        catLabel(cat) {
            const map = {
                '文件共享': 'component.file_sharing',
                '对象存储': 'component.object_storage',
                '网页管理': 'component.web_management',
                '系统防护': 'component.system_protection',
                '存储管理': 'component.storage_mgmt',
                '运行环境': 'component.runtime_env',
            };
            return map[cat] ? window.t(map[cat]) : cat;
        },

        // 存储池/卷 后端生成的显示名（存储池N/卷N）翻译
        poolLabel(name) {
            if (!name) return name;
            const m = name.match(/^存储池(\d+)$/);
            if (m) return window.t('storage.pool_n', [m[1]]);
            const v = name.match(/^卷(\d+)$/);
            if (v) return window.t('storage.vol_n', [v[1]]);
            return name;
        },
        // 存储池类型标签（仪表盘池卡片副标题）：raid 显示级别，lvm 按盘数区分单盘/合并
        poolTypeLabel(pool) {
            const t = ((pool && pool.type) || '').toLowerCase();
            if (t.indexOf('raid') === 0) return t.toUpperCase();
            if (t === 'lvm') {
                const n = ((pool && pool.disks) || []).length;
                return n > 1 ? window.t('storage.pool_type_lvm_merge') : window.t('storage.pool_type_lvm_single');
            }
            if (t === 'single') return window.t('storage.single');
            return t ? t.toUpperCase() : '';
        },
        // 告警渠道名翻译
        channelLabel(name) {
            const map = { '钉钉': 'monitor.channel_dingtalk', 'Email': 'monitor.channel_email' };
            return map[name] ? window.t(map[name]) : name;
        },
        // 系统设置页服务列表显示名翻译（systemd 服务名 → i18n key）
        svcListDisplay(name, fallback) {
            const map = {
                'smbd': 'services.samba',
                'nmbd': 'services.netbios',
                'vsftpd': 'services.ftp_svc',
                'nfs-server': 'services.nfs_svc',
                'nas-panel': 'services.nas_panel',
                'fail2ban': 'services.fail2ban',
                'cron': 'services.cron',
                'ssh': 'services.ssh_svc',
            };
            return map[name] ? window.t(map[name]) : fallback;
        },
        // RAID 方案字段翻译（按 id / safety 映射后端中文）
        raidName(id, fallback) {
            window.Alpine.store('i18n').lang;
            const k = 'storage.raid_name_' + id;
            return window.t(k) !== k ? window.t(k) : (fallback || id);
        },
        raidDesc(id, fallback) {
            window.Alpine.store('i18n').lang;
            const k = 'storage.raid_desc_' + id;
            return window.t(k) !== k ? window.t(k) : (fallback || '');
        },
        raidWarn(id, fallback) {
            window.Alpine.store('i18n').lang;
            const k = 'storage.raid_warn_' + id;
            return window.t(k) !== k ? window.t(k) : (fallback || '');
        },
        safetyLabel(level, fallback) {
            window.Alpine.store('i18n').lang;
            const k = 'storage.safety_' + level;
            return window.t(k) !== k ? window.t(k) : (fallback || '');
        },
        // 组件详情翻译（version.go detail 字段，按 name 映射）
        compDetail(name, fallback) {
            const map = {
                'Samba (smbd)': 'component.detail_smbd',
                'NFS (nfs-kernel-server)': 'component.detail_nfs',
                'vsftpd': 'component.detail_ftp',
                'rclone': 'component.detail_rclone',
                'Fail2ban': 'component.detail_fail2ban',
                'UFW': 'component.detail_ufw',
                'LVM': 'component.detail_lvm',
                'mdadm': 'component.detail_mdadm',
                'xfsprogs': 'component.detail_xfs',
                'smartmontools': 'component.detail_smart',
                'Linux 内核': 'component.detail_kernel',
                '操作系统': 'component.detail_os',
            };
            if (map[name]) return window.t(map[name]);
            if (name === 'nas-panel') {
                const p = this.components && this.components.panel ? this.components.panel : {};
                return window.t('component.detail_panel', [p.build_time || '', p.git_commit || '']);
            }
            return fallback || '';
        },
        // sysctl 描述翻译（system.go，按 key 映射）
        sysctlDesc(key, fallback) {
            const map = {
                'vm.swappiness': 'system.sysctl_swappiness',
                'vm.dirty_ratio': 'system.sysctl_dirty_ratio',
                'vm.dirty_background_ratio': 'system.sysctl_dirty_bg',
                'net.core.somaxconn': 'system.sysctl_somaxconn',
                'net.ipv4.tcp_congestion_control': 'system.sysctl_congestion',
                'fs.file-max': 'system.sysctl_filemax',
            };
            return map[key] ? window.t(map[key]) : (fallback || '');
        },
        // 诊断项 name/desc/duration 翻译（按 id 映射）
        diagName(id, fallback) {
            const map = { 'smart_short': 'diagnostics.diag_smart_short', 'smart_long': 'diagnostics.diag_smart_long', 'raid_scrub': 'diagnostics.diag_raid_scrub' };
            return map[id] ? window.t(map[id]) : (fallback || id);
        },
        diagDesc(id, fallback) {
            const map = { 'smart_short': 'diagnostics.diag_smart_short_desc', 'smart_long': 'diagnostics.diag_smart_long_desc', 'raid_scrub': 'diagnostics.diag_raid_scrub_desc' };
            return map[id] ? window.t(map[id]) : (fallback || '');
        },
        diagDuration(id, fallback) {
            const map = { 'smart_short': 'diagnostics.diag_smart_short_dur', 'smart_long': 'diagnostics.diag_smart_long_dur', 'raid_scrub': 'diagnostics.diag_raid_scrub_dur' };
            return map[id] ? window.t(map[id]) : (fallback || '');
        },

        async api(path, options = {}) {
            const opts = {
                ...options,
                headers: {
                    'Authorization': 'Bearer ' + this.token,
                    ...(options.headers || {})
                }
            };
            const res = await fetch('/api' + path, opts);
            if (res.status === 401) {
                this.logout();
                return null;
            }
            const ct = res.headers.get('content-type');
            if (ct && ct.includes('application/json')) {
                const data = await res.json();
                if (!res.ok && data.error) {
                    this.showToast(data.error, 'error');
                    return null;
                }
                return data;
            }
            return res.text();
        },

        async login() {
            this.loading = true;
            this.loginError = '';
            try {
                let body = `username=${encodeURIComponent(this.loginForm.username)}&password=${encodeURIComponent(this.loginForm.password)}`;
                if (this.loginForm.totp) body += `&totp=${encodeURIComponent(this.loginForm.totp)}`;
                const res = await fetch('/api/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: body
                });
                const data = await res.json();
                if (res.ok) {
                    if (data['2fa_required']) {
                        this.needTotp = true;
                        return;
                    }
                    this.token = data.token;
                    localStorage.setItem('nas_token', this.token);
                    this.needTotp = false;
                    this.loginForm.totp = '';
                    if (data.must_change_password) {
                        this.mustChangePassword = true;
                        this.pwdUser = data.username || this.loginForm.username;
                        this.pwdForm = { password: '' };
                    } else {
                        this.navigate('dashboard');
                    }
                } else {
                    this.loginError = data.error || this.t('msg.login_failed');
                }
            } catch (e) {
                this.loginError = this.t('msg.network_error') + ': ' + e.message;
            } finally {
                this.loading = false;
            }
        },

        logout() {
            this.token = '';
            localStorage.removeItem('nas_token');
            this.page = 'dashboard';
            this.stopNotifPolling();
        },

        navigate(page) {
            if (this.page === 'vault' && page !== 'vault') this.vaultLock();
            if (this.page === 'ai' && page !== 'ai') this.stopTaskJobsPolling();
            this.page = page;
            switch (page) {
                case 'dashboard': this.loadDashboard(); break;
                case 'services': this.loadServices(); break;
                case 'users': this.loadUsers(); this.loadUserGroups(); this.loadPermMatrix(); this.loadLoginLogs(); break;
                case 'diskmgmt': this.loadStorageOverview(); this.loadWizardStatus(); this.loadSharedFolders(); this.loadPendingOps(); this.loadUsers(); this.loadImportablePools(); break;
                case 'files': this.loadFiles(''); break;
                case 'firewall': this.loadFirewall(); break;
                case 'monitor': this.initMonitorRefresh(); this.loadAlertConfig(); break;
                case 'system': this.loadSystemOverview(); this.load2FAStatus(); this.checkPanelUpdate(); break;
                case 'about': this.loadComponents(); this.loadHardware(); break;
                case 'notice': this.loadNotice(); break;
                case 'backup': this.loadBackups(); break;
                case 'vault': this.vaultOnEnter(); break;
                case 'pwgen': this.pwgenOnEnter(); break;
                case 'ai': this.loadAIStatus(); this.loadAIConversations(); this.loadAITasks(); break;
                case 'rclone': this.loadRcloneStatus(); this.loadRcloneRemotes(); this.loadRcloneTasks().then(() => { if (this.rcloneRunningTasks.length) this.startRcloneProgressPolling(); }); this.loadRcloneLogs(); this.loadSharedDirs(); break;
                case 'logs': this.loadAuditLogs(); break;
                case 'events': this.loadEvents(); this.loadEventStats(); break;
                case 'diagnostics': this.loadDiagnostics(); break;
                case 'health': this.loadHealth(); break;
            }
        },

        async loadDashboard() {
            this.dashboardLoaded = false;
            const data = await this.api('/dashboard');
            if (data) this.dashboard = data;
            // 刷新/恢复会话后仍强制改密（登录响应只在登录那一次带 must_change_password）
            if (data && data.must_change_password) {
                this.mustChangePassword = true;
                if (!this.pwdUser) this.pwdUser = data.username || this.loginForm.username || '';
            }
            // Also load storage overview for disk bay diagram
            const sdata = await this.api('/disk/overview');
            if (sdata && sdata.overview) this.storageOverview = sdata.overview;
            // Check config consistency
            const cdata = await this.api('/disk/config/check');
            if (cdata) this.configIssues = { has_issues: !!cdata.has_issues, issues: cdata.issues || [] };
            // 最近告警摘要（失败静默，不阻塞面板）
            this.loadRecentAlerts().catch(() => {});
            this.dashboardLoaded = true;
        },

        async loadHealth() {
            this.healthLoaded = false;
            const data = await this.api('/health');
            if (data) this.health = data;
            this.healthLoaded = true;
        },

        healthLabel(id, fallback) {
            const k = 'health.' + id;
            return window.t(k) !== k ? window.t(k) : (fallback || id);
        },

        healthStatusColor(status) {
            return { ok: '#16a34a', info: '#64748b', warn: '#d97706', error: '#dc2626' }[status] || '#64748b';
        },

        healthStatusText(status) {
            const k = 'health.' + status;
            return window.t(k) !== k ? window.t(k) : status;
        },

        get serviceSummary() {
            // not-installed = 可选服务未装,不是异常也不计入总数,
            // 否则全新安装仪表盘恒显「1 个异常」假告警。
            const svcs = (this.dashboard.services || []).filter(s => s.active !== 'not-installed');
            const down = svcs.filter(s => s.active !== 'active').length;
            return { total: svcs.length, down };
        },

        // RAID 类型存储池（替换盘/RAID 扩容只用 mdadm，仅对 RAID 池有效）
        get raidPools() {
            return (this.storageOverview.pools || []).filter(p => (p.type || '').toLowerCase().indexOf('raid') === 0);
        },

        // 替换盘弹窗当前选中池的成员盘（作为「故障盘」候选项）
        get replaceMemberDisks() {
            const p = this.raidPools.find(x => x.name === this.replaceDiskForm.pool_name);
            return p ? (p.disks || []) : [];
        },

        async loadServices() {
            const data = await this.api('/services');
            if (data) this.services = data.services || [];
        },

        async installServices() {
            if (!confirm(this.t('msg.install_all_confirm'))) return;
            this.installingServices = true;
            this.installMsg = this.t('msg.installing');
            const data = await this.api('/services/install', { method: 'POST' });
            if (data) {
                if (data.error) {
                    this.showToast(this.t('msg.install_failed') + ': ' + data.error, 'error');
                    this.installMsg = this.t('msg.install_failed') + ': ' + data.error;
                } else {
                    this.installMsg = this.t('msg.install_done');
                    this.showToast(this.t('msg.install_done'), 'success');
                    setTimeout(() => this.loadServices(), 2000);
                }
            } else {
                this.installMsg = this.t('msg.install_failed');
            }
            this.installingServices = false;
        },

        async installService(name) {
            if (!confirm(this.t('msg.install_x_confirm', [name]))) return;
            this.installMsg = this.t('msg.installing_x', [name]);
            const data = await this.api('/services/install', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `service=${encodeURIComponent(name)}`
            });
            if (data) {
                if (data.error) {
                    this.showToast(this.t('msg.install_failed') + ': ' + data.error, 'error');
                    this.installMsg = this.t('msg.install_x_failed', [name]) + ': ' + data.error;
                } else {
                    this.showToast(this.t('msg.x_install_done', [name]), 'success');
                    this.installMsg = this.t('msg.x_install_done', [name]);
                    setTimeout(() => this.loadServices(), 2000);
                }
            } else {
                this.installMsg = this.t('msg.install_x_failed', [name]);
            }
        },

        async loadUsers() {
            const data = await this.api('/users');
            if (data) this.users = data.users || [];
        },

        async loadStorage() {
            const data = await this.api('/storage');
            if (data) this.storage = data;
        },

        async loadSmart() {
            this.smartStatus = this.t('msg.checking');
            const data = await this.api('/storage/smart');
            if (data) this.smartStatus = data;
        },

        async loadFirewall() {
            const data = await this.api('/firewall');
            if (data && !data.error) {
                data.loaded = true;
                data.rules = data.rules || [];
                this.firewall = data;
            }
        },

        async svcAction(name, action) {
            const data = await this.api(`/services/${name}/${action}`, { method: 'POST' });
            if (data) {
                this.showToast(this.t('msg.op_success'), 'success');
                this.loadServices();
            }
        },

        async showLogs(name) {
            this.logsService = name;
            this.logsContent = this.t('common.loading');
            this.logsModal = true;
            const data = await this.api(`/services/${name}/logs`);
            if (data) this.logsContent = data || this.t('msg.no_logs');
        },

        showAddUser() {
            this.addUserForm = { username: '', password: '' };
            this.addUserModal = true;
        },

        async addUser() {
            if (!this.addUserForm.username || !this.addUserForm.password) {
                this.showToast(this.t('msg.fill_user_pass'), 'error');
                return;
            }
            if (this.addUserForm.password.length < 12) {
                this.showToast(this.t('msg.pwd_min_12'), 'error');
                return;
            }
            const data = await this.api('/users', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `username=${encodeURIComponent(this.addUserForm.username)}&password=${encodeURIComponent(this.addUserForm.password)}`
            });
            if (data) {
                this.showToast(this.t('msg.add_success'), 'success');
                this.addUserModal = false;
                this.loadUsers();
            }
        },

        showChangePassword(username) {
            this.pwdUser = username;
            this.pwdForm = { password: '' };
            this.pwdModal = true;
        },

        async changePassword() {
            if (this.pwdForm.password.length < 12) {
                this.showToast(this.t('msg.pwd_min_12'), 'error');
                return;
            }
            const data = await this.api(`/users/${this.pwdUser}/password`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `password=${encodeURIComponent(this.pwdForm.password)}`
            });
            if (data) {
                this.showToast(this.t('msg.modify_success'), 'success');
                this.pwdModal = false;
                if (this.mustChangePassword) {
                    this.mustChangePassword = false;
                    this.navigate('dashboard');
                }
            }
        },

        async deleteUser(username) {
            if (!confirm(this.t('msg.del_user_confirm', [username]))) return;
            const data = await this.api(`/users/${username}?delete_data=false`, { method: 'DELETE' });
            if (data) {
                this.showToast(this.t('msg.del_success'), 'success');
                this.loadUsers();
            }
        },

        // ===== Users module - new functions =====

        async loadUserGroups() {
            const data = await this.api('/user-groups');
            if (data) this.userGroups = data.groups || [];
        },

        async loadPermMatrix() {
            const data = await this.api('/users-matrix');
            if (data) this.permMatrix = data;
        },

        async loadLoginLogs() {
            this.loginLogsLoading = true;
            const data = await this.api('/users-login-log?limit=50');
            if (data) this.loginLogs = data.entries || [];
            this.loginLogsLoading = false;
        },

        // User wizard
        openUserWizard() {
            this.userWizardStep = 1;
            this.userWizardForm = {
                username: '', password: '', password2: '',
                svc_samba: true, svc_ftp: true, svc_webdav: true,
                quota_gb: 0,
                sharePerms: {}
            };
            // Load available shares for step 4
            this.userWizardShares = this.permMatrix.folders || [];
            for (const f of this.userWizardShares) {
                this.userWizardForm.sharePerms[f] = 'noaccess';
            }
            this.addUserModal = true;
        },

        wizardNext() {
            if (this.userWizardStep === 1) {
                if (!this.userWizardForm.username) {
                    this.showToast(this.t('msg.enter_username'), 'error'); return;
                }
                if (this.userWizardForm.password.length < 12) {
                    this.showToast(this.t('msg.pwd_min_12'), 'error'); return;
                }
                if (this.userWizardForm.password !== this.userWizardForm.password2) {
                    this.showToast(this.t('msg.pwd_mismatch'), 'error'); return;
                }
            }
            if (this.userWizardStep < 4) this.userWizardStep++;
        },

        wizardPrev() {
            if (this.userWizardStep > 1) this.userWizardStep--;
        },

        async wizardSubmit() {
            const f = this.userWizardForm;
            let body = `username=${encodeURIComponent(f.username)}&password=${encodeURIComponent(f.password)}`;
            body += `&svc_samba=${f.svc_samba}&svc_ftp=${f.svc_ftp}&svc_webdav=${f.svc_webdav}`;
            body += `&quota_gb=${f.quota_gb}`;
            for (const [folder, perm] of Object.entries(f.sharePerms)) {
                body += `&share_${encodeURIComponent(folder)}=${perm}`;
            }
            const data = await this.api('/users', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            if (data) {
                this.showToast(this.t('msg.create_success'), 'success');
                this.addUserModal = false;
                this.loadUsers();
                this.loadPermMatrix();
            }
        },

        // Service toggle
        async toggleUserService(username, service, enabled) {
            const body = `${service}=${enabled}`;
            const data = await this.api(`/users/${username}/services`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            if (data) {
                this.showToast(this.t('msg.perm_updated'), 'success');
                this.loadUsers();
            }
        },

        // Quota
        async setUserQuota(username, quotaGB) {
            const data = await this.api(`/users/${username}/quota`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `quota_gb=${quotaGB}`
            });
            if (data) {
                this.showToast(this.t('msg.quota_updated'), 'success');
                this.loadUsers();
            }
        },

        // Groups
        openGroupModal() {
            this.groupForm = { name: '', comment: '', members: '' };
            this.showGroupModal = true;
        },

        async createGroup() {
            if (!this.groupForm.name) {
                this.showToast(this.t('msg.enter_group'), 'error'); return;
            }
            const data = await this.api('/user-groups', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `name=${encodeURIComponent(this.groupForm.name)}&comment=${encodeURIComponent(this.groupForm.comment)}`
            });
            if (data) {
                this.showToast(this.t('msg.create_success'), 'success');
                this.showGroupModal = false;
                this.loadUserGroups();
            }
        },

        async deleteGroup(name) {
            if (!confirm(this.t('msg.del_group_confirm', [name]))) return;
            const data = await this.api(`/user-groups/${name}`, { method: 'DELETE' });
            if (data) {
                this.showToast(this.t('msg.del_success'), 'success');
                this.loadUserGroups();
            }
        },

        async updateGroupMembers(name, members) {
            const data = await this.api(`/user-groups/${name}/members`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `members=${encodeURIComponent(members)}`
            });
            if (data) {
                this.showToast(this.t('msg.members_updated'), 'success');
                this.loadUserGroups();
            }
        },

        // Matrix permission
        async setMatrixPerm(username, folder, perm) {
            const data = await this.api('/users-matrix', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `username=${encodeURIComponent(username)}&folder=${encodeURIComponent(folder)}&permission=${perm}`
            });
            if (data) {
                this.showToast(this.t('msg.perm_matrix_updated'), 'success');
                this.loadPermMatrix();
            }
        },

        applyPreset(p) {
            this.firewallForm.port = p.port;
            this.firewallForm.proto = p.proto;
            this.firewallForm.action = 'allow';
            if (!this.firewallForm.comment) this.firewallForm.comment = p.label.replace(/\s*\(.*\)/, '');
        },

        async addFirewallRule() {
            const f = this.firewallForm;
            if (!f.port) {
                this.showToast(this.t('msg.enter_port'), 'error');
                return;
            }
            this.firewallSaving = true;
            const body = `port=${encodeURIComponent(f.port)}&proto=${encodeURIComponent(f.proto)}&action=${encodeURIComponent(f.action)}&from=${encodeURIComponent(f.from)}&comment=${encodeURIComponent(f.comment)}`;
            const data = await this.api('/firewall/rules', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            this.firewallSaving = false;
            if (data && !data.error) {
                this.showToast(this.t('msg.rule_added'), 'success');
                this.firewallForm = { port: '', proto: 'tcp', action: 'allow', from: '', comment: '' };
                this.loadFirewall();
            } else if (data && data.error) {
                this.showToast(this.t('msg.add_failed') + ': ' + data.error, 'error');
            }
        },

        async deleteFirewallRule(rule) {
            const desc = (rule.proto === 'any' ? rule.port : rule.port + '/' + rule.proto) + (rule.v6 ? ' (v6)' : '');
            if (!confirm(this.t('msg.del_rule_confirm', [rule.num, desc]))) return;
            const data = await this.api('/firewall/rules/' + rule.num, { method: 'DELETE' });
            if (data && !data.error) {
                this.showToast(this.t('msg.rule_deleted'), 'success');
                this.loadFirewall();
            } else if (data && data.error) {
                this.showToast(this.t('msg.del_failed') + ': ' + data.error, 'error');
            }
        },

        async toggleFirewall() {
            const enabling = !this.firewall.active;
            if (enabling && !confirm(this.t('msg.enable_fw_confirm'))) return;
            if (!enabling && !confirm(this.t('msg.disable_fw_confirm'))) return;
            const data = await this.api('/firewall/' + (enabling ? 'enable' : 'disable'), { method: 'POST' });
            if (data && !data.error) {
                this.showToast(this.t('msg.op_success'), 'success');
                this.loadFirewall();
            } else if (data && data.error) {
                this.showToast(this.t('msg.op_failed') + ': ' + data.error, 'error');
            }
        },

        gaugeColor(pct) {
            if (pct >= 90) return '#dc2626';
            if (pct >= 70) return '#ea580c';
            if (pct >= 50) return '#d97706';
            return '#16a34a';
        },

        formatNetRate(bytesPerSec) {
            if (!bytesPerSec || bytesPerSec <= 0) return '0 B/s';
            const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
            let i = 0;
            let val = parseFloat(bytesPerSec);
            while (val >= 1024 && i < units.length - 1) { val /= 1024; i++; }
            return val.toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
        },

        showToast(msg, type) {
            this.toast = { show: true, msg, type };
            setTimeout(() => { this.toast.show = false; }, 3000);
        },

        async loadMonitor() {
            const data = await this.api('/monitor');
            if (data) this.monitor = data;
            this.loadConnections();
        },

        async loadConnections() {
            const data = await this.api('/monitor/connections');
            if (data) this.connections = data.connections || [];
        },

        // ═══ 回收站（per-share，TODO #25）═══
        async loadRecycleBin() {
            const q = this.recycleShareFilter ? '?share=' + encodeURIComponent(this.recycleShareFilter) : '';
            const data = await this.api('/disk/recycle' + q);
            if (data) {
                this.recycleItems = data.items || [];
                this.recycleShares = data.shares || {};
            }
        },

        async restoreRecycleItem(it) {
            const params = new URLSearchParams({ share: it.share, rel_path: it.rel_path, user: it.user || '' });
            if (it.legacy) params.append('legacy', 'yes');
            const data = await this.api('/disk/recycle/restore', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data && !data.error) {
                this.showToast(this.t('msg.op_success'), 'success');
                this.loadRecycleBin();
                this.loadSharedFolders();
                this.loadStorageOverview();
            }
        },

        async deleteRecycleItem(it) {
            if (!confirm(this.t('storage.recycle_delete_confirm', [it.rel_path]))) return;
            const params = new URLSearchParams({ share: it.share, rel_path: it.rel_path, user: it.user || '', confirm: 'yes' });
            if (it.legacy) params.append('legacy', 'yes');
            const data = await this.api('/disk/recycle/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data && !data.error) {
                this.showToast(this.t('msg.del_success'), 'success');
                this.loadRecycleBin();
            }
        },

        async clearRecycleBin() {
            const share = this.recycleShareFilter;
            if (!share) return;
            const typed = prompt(this.t('storage.recycle_clear_confirm', [share]));
            if (typed !== share) {
                if (typed !== null) this.showToast(this.t('msg.name_mismatch'), 'error');
                return;
            }
            const params = new URLSearchParams({ share, confirm_name: share });
            const data = await this.api('/disk/recycle/clear', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data && !data.error) {
                this.showToast(this.t('msg.del_success'), 'success');
                this.loadRecycleBin();
            }
        },

        formatSize(bytes) {
            if (bytes === undefined || bytes === null) return '-';
            if (bytes < 1024) return bytes + ' B';
            const units = ['KB', 'MB', 'GB', 'TB'];
            let v = bytes, i = -1;
            do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
            return v.toFixed(v >= 100 ? 0 : 1) + ' ' + units[i];
        },

        // ═══ 文件管理器(只读 v1) ═══
        async loadFiles(rel) {
            const changingDir = this.filesPage.path !== rel;
            this.filesPage.loading = true;
            try {
                const data = await this.api('/files/list?path=' + encodeURIComponent(rel));
                if (data) {
                    this.filesPage.path = rel;
                    this.filesPage.entries = data.entries || [];
                    this.filesPage.breadcrumb = data.breadcrumb || [];
                    this.filesPage.truncated = !!data.truncated;
                    if (changingDir) this.filesPage.selected = [];
                    // 用量懒加载,不阻塞列表
                    this.loadFilesUsage(rel);
                }
            } finally {
                this.filesPage.loading = false;
            }
        },
        // du_status 三态(Task 4 review Important#1):ok=bytes 有值;timeout/error=bytes 为 null,
        // 前端对非 ok 一律降级展示"用量不可用",不 switch 具体态
        async loadFilesUsage(rel) {
            const d = await this.api('/files/usage?path=' + encodeURIComponent(rel));
            if (d && d.pool) {
                this.filesPage.usageText = `${this.formatSize(d.pool.used_kb * 1024)} / ${this.formatSize(d.pool.total_kb * 1024)}`;
            } else {
                this.filesPage.usageText = this.t('files.usage_unavailable');
            }
        },
        toggleFileSelection(entry) {
            const selected = this.filesPage.selected;
            const index = selected.findIndex(e => e.name === entry.name);
            if (index >= 0) selected.splice(index, 1);
            else selected.push({ name: entry.name, type: entry.type });
        },
        isFileSelected(entry) {
            return this.filesPage.selected.some(e => e.name === entry.name);
        },
        toggleAllFiles() {
            const entries = this.filesPage.entries;
            if (this.filesPage.selected.length === entries.length) this.filesPage.selected = [];
            else this.filesPage.selected = entries.map(e => ({ name: e.name, type: e.type }));
        },
        clearFileSelection() {
            this.filesPage.selected = [];
        },
        async fileWrite(path, body) {
            const res = await fetch('/api/files' + path, {
                method: 'POST',
                headers: { 'Authorization': 'Bearer ' + this.token, 'Content-Type': 'application/json' },
                body: JSON.stringify(body)
            });
            // Minor②:对齐 api()(约 475 行)既有的 401 处理 — 登出并中断调用方后续逻辑
            if (res.status === 401) {
                this.logout();
                throw new Error(this.t('msg.op_failed'));
            }
            if (!res.ok) throw new Error(await res.text());
            return res;
        },
        openMkdir() {
            this.filesPage.mkdirName = '';
            this.filesPage.mkdirError = '';
            this.filesPage.showMkdir = true;
        },
        async createFileDirectory() {
            const name = this.filesPage.mkdirName.trim();
            if (!name || name === '.' || name === '..' || name.includes('/') || name.includes('..')) {
                this.filesPage.mkdirError = this.t('files.invalid_name');
                return;
            }
            try {
                await this.fileWrite('/mkdir', { path: this.filesPage.path, name });
                this.filesPage.showMkdir = false;
                this.showToast(this.t('msg.op_success'), 'success');
                await this.loadFiles(this.filesPage.path);
            } catch (err) {
                this.filesPage.mkdirError = String(err.message || err);
                this.showToast(this.filesPage.mkdirError, 'error');
            }
        },
        openRename(entry) {
            this.filesPage.renameEntry = entry;
            this.filesPage.renameName = entry.name;
            this.filesPage.renameError = '';
            this.filesPage.showRename = true;
        },
        async renameFile() {
            const name = this.filesPage.renameName.trim();
            const entry = this.filesPage.renameEntry;
            if (!name || name === '.' || name === '..' || name.includes('/') || name.includes('..')) {
                this.filesPage.renameError = this.t('files.invalid_name');
                return;
            }
            try {
                await this.fileWrite('/rename', { path: this.filesPage.relJoin(entry.name), new_name: name });
                this.filesPage.showRename = false;
                // Minor③:rename 成功后从 selected 中剔除旧名条目(同目录刷新不会清空 selected,
                // 残留旧名会导致后续批量移动/删除按旧名请求 404)。只移除被改名的条目,保留其余选择。
                this.filesPage.selected = this.filesPage.selected.filter(e => e.name !== entry.name);
                this.showToast(this.t('msg.op_success'), 'success');
                await this.loadFiles(this.filesPage.path);
            } catch (err) {
                this.filesPage.renameError = String(err.message || err);
                this.showToast(this.filesPage.renameError, 'error');
            }
        },
        selectUploadFiles() {
            this.$refs.fileUploadInput.click();
        },
        selectUploadDir() {
            this.$refs.dirUploadInput.click();
        },
        // 上传/批量操作失败 responseText 可能是 JSON({"error":"..."})或纯文本,统一解析取 .error,解析失败兜底原文
        parseErrorText(text) {
            try {
                const data = JSON.parse(text);
                if (data && data.error) return data.error;
            } catch (e) {}
            return text;
        },
        uploadFiles(input) {
            const files = Array.from(input.files || []);
            if (!files.length) return;
            const form = new FormData();
            // 目录上传(webkitdirectory)时 file.webkitRelativePath 携带子路径(如 "d1/a.txt"),
            // 后端按该 filename 逐段消毒并建父目录;普通文件该属性为空串,回退 file.name,行为不变。
            files.forEach(file => form.append('files', file, file.webkitRelativePath || file.name));
            // Important#1 修复:先赋值到 filesPage.uploadProgress,再取引用,确保 progress 是 Alpine 响应式代理
            // 而非链式赋值 `const progress = this.filesPage.uploadProgress = {...}` 捕获到的 raw 对象
            this.filesPage.uploadProgress = { active: true, percent: 0, loaded: 0, total: files.reduce((sum, file) => sum + file.size, 0) };
            const progress = this.filesPage.uploadProgress;
            const xhr = new XMLHttpRequest();
            const url = '/api/files/upload?path=' + encodeURIComponent(this.filesPage.path);
            xhr.open('POST', url);
            xhr.setRequestHeader('Authorization', 'Bearer ' + this.token);
            xhr.upload.onprogress = event => {
                if (event.lengthComputable) {
                    progress.loaded = event.loaded;
                    progress.total = event.total;
                    progress.percent = Math.round(event.loaded / event.total * 100);
                }
            };
            xhr.onload = () => {
                input.value = '';
                if (xhr.status === 409) {
                    let conflict = '';
                    try { conflict = JSON.parse(xhr.responseText).conflict || this.parseErrorText(xhr.responseText) || ''; } catch (e) {}
                    if (confirm(this.t('files.conflict_overwrite_confirm', [conflict]))) {
                        this.uploadFilesWithOverwrite(files);
                        return;
                    }
                    this.filesPage.uploadProgress = { active: false, percent: 0, loaded: 0, total: 0 };
                    this.showToast(this.parseErrorText(xhr.responseText), 'error');
                    return;
                }
                if (xhr.status < 200 || xhr.status >= 300) {
                    this.filesPage.uploadProgress = { active: false, percent: 0, loaded: 0, total: 0 };
                    this.showToast(this.parseErrorText(xhr.responseText) || this.t('msg.op_failed'), 'error');
                    return;
                }
                this.filesPage.uploadProgress.percent = 100;
                this.filesPage.uploadProgress.active = false;
                this.showToast(this.t('msg.op_success'), 'success');
                this.loadFiles(this.filesPage.path);
            };
            xhr.onerror = () => {
                input.value = '';
                this.filesPage.uploadProgress = { active: false, percent: 0, loaded: 0, total: 0 };
                this.showToast(this.t('msg.op_failed'), 'error');
            };
            xhr.send(form);
        },
        uploadFilesWithOverwrite(files) {
            const form = new FormData();
            // 409 重试同样要保留子路径,否则目录上传冲突重试会把文件拍平到目标目录。
            files.forEach(file => form.append('files', file, file.webkitRelativePath || file.name));
            const xhr = new XMLHttpRequest();
            xhr.open('POST', '/api/files/upload?path=' + encodeURIComponent(this.filesPage.path) + '&overwrite=yes');
            xhr.setRequestHeader('Authorization', 'Bearer ' + this.token);
            xhr.upload.onprogress = event => {
                if (event.lengthComputable) {
                    this.filesPage.uploadProgress.loaded = event.loaded;
                    this.filesPage.uploadProgress.total = event.total;
                    this.filesPage.uploadProgress.percent = Math.round(event.loaded / event.total * 100);
                }
            };
            xhr.onload = () => {
                this.filesPage.uploadProgress.active = false;
                if (xhr.status < 200 || xhr.status >= 300) {
                    this.showToast(this.parseErrorText(xhr.responseText) || this.t('msg.op_failed'), 'error');
                    return;
                }
                this.showToast(this.t('msg.op_success'), 'success');
                this.loadFiles(this.filesPage.path);
            };
            xhr.onerror = () => {
                this.filesPage.uploadProgress.active = false;
                this.showToast(this.t('msg.op_failed'), 'error');
            };
            xhr.send(form);
        },
        async downloadFile(e) {
            const rel = this.filesPage.relJoin(e.name);
            try {
                const res = await fetch('/api/files/download?path=' + encodeURIComponent(rel), {
                    headers: { 'Authorization': 'Bearer ' + this.token }
                });
                if (!res.ok) throw new Error(await res.text());
                const blob = await res.blob();
                const objUrl = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = objUrl;
                a.download = e.name;
                a.click();
                setTimeout(() => URL.revokeObjectURL(objUrl), 1000);
            } catch (err) {
                this.showToast(String(err.message || err), 'error');
            }
        },
        previewable(e) {
            if (e.type !== 'file') return false;
            const ext = ('.' + e.name.split('.').pop()).toLowerCase();
            return ['.txt', '.md', '.json', '.yaml', '.yml', '.log', '.conf', '.ini', '.csv', '.sh',
                '.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp'].includes(ext) && e.size <= 10 * 1024 * 1024;
        },
        closePreview() {
            if (this.filesPage.preview.url) URL.revokeObjectURL(this.filesPage.preview.url);
            this.filesPage.preview = { open: false, kind: '', name: '', content: '', url: '' };
        },
        async previewFile(e) {
            const rel = this.filesPage.relJoin(e.name);
            const ext = ('.' + e.name.split('.').pop()).toLowerCase();
            const imgs = ['.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp'];
            const kind = imgs.includes(ext) ? 'image' : 'text';
            if (this.filesPage.preview.url) URL.revokeObjectURL(this.filesPage.preview.url);
            this.filesPage.preview = { open: true, kind, name: e.name, content: '', url: '' };
            try {
                const res = await fetch('/api/files/preview?path=' + encodeURIComponent(rel), {
                    headers: { 'Authorization': 'Bearer ' + this.token }
                });
                if (!res.ok) throw new Error(await res.text());
                if (kind === 'image') {
                    const blob = await res.blob();
                    this.filesPage.preview.url = URL.createObjectURL(blob);
                } else {
                    const d = await res.json();
                    this.filesPage.preview.content = d.content + (d.truncated ? this.t('files.preview_truncated') : '');
                }
            } catch (err) {
                this.showToast(String(err.message || err), 'error');
                this.filesPage.preview.open = false;
            }
        },
        // ═══ 移动/复制目录选择器 ═══
        openTransferDialog(mode, singleEntry) {
            this.filesPage.transferMode = mode; // 'move' | 'copy'
            this.filesPage.transferEntries = singleEntry ? [singleEntry] : this.filesPage.selected.slice();
            this.filesPage.transferPickerEntries = [];
            this.filesPage.showTransfer = true;
            this.transferPickerNavigate('');
        },
        async transferPickerNavigate(path) {
            const data = await this.api('/files/list?path=' + encodeURIComponent(path));
            if (!data) return;
            this.filesPage.transferPath = path;
            this.filesPage.transferBreadcrumb = data.breadcrumb || [];
            this.filesPage.transferPickerEntries = data.entries || [];
        },
        transferPickerDirs() {
            // 目录选择器只展示当前浏览目录下的子目录;过滤 .recycle 防止把文件移动/复制进回收站干扰 restore 语义(Minor④)
            return (this.filesPage.transferPickerEntries || []).filter(e => e.type === 'dir' && e.name !== '.recycle');
        },
        async confirmTransfer() {
            const mode = this.filesPage.transferMode;
            const dstDir = this.filesPage.transferPath;
            const entries = this.filesPage.transferEntries;
            let successCount = 0;
            for (const entry of entries) {
                try {
                    await this.fileWrite('/' + mode, { src: this.filesPage.relJoin(entry.name), dst_dir: dstDir });
                    successCount++;
                } catch (err) {
                    this.showToast(this.t('files.batch_partial_fail', [successCount, entries.length, String(err.message || err)]), 'error');
                    this.filesPage.showTransfer = false;
                    this.filesPage.selected = [];
                    await this.loadFiles(this.filesPage.path);
                    return;
                }
            }
            this.filesPage.showTransfer = false;
            this.filesPage.selected = [];
            this.showToast(this.t('msg.op_success'), 'success');
            await this.loadFiles(this.filesPage.path);
        },
        // ═══ 删除确认(type-to-confirm 对齐危险操作模式) ═══
        openDeleteDialog(singleEntry) {
            const entries = singleEntry ? [singleEntry] : this.filesPage.selected.slice();
            if (!entries.length) return;
            this.filesPage.deleteEntries = entries;
            this.filesPage.deleteConfirmName = '';
            this.filesPage.deleteError = '';
            const firstDir = entries.find(e => e.type === 'dir');
            this.filesPage.deleteRequiredName = firstDir ? firstDir.name : '';
            this.filesPage.showDelete = true;
        },
        deleteConfirmDisabled() {
            if (!this.filesPage.deleteRequiredName) return false;
            return this.filesPage.deleteConfirmName !== this.filesPage.deleteRequiredName;
        },
        async confirmDelete() {
            if (this.deleteConfirmDisabled()) return;
            const entries = this.filesPage.deleteEntries;
            let successCount = 0;
            for (const entry of entries) {
                try {
                    await this.fileWrite('/delete', { path: this.filesPage.relJoin(entry.name) });
                    successCount++;
                } catch (err) {
                    this.showToast(this.t('files.batch_partial_fail', [successCount, entries.length, String(err.message || err)]), 'error');
                    this.filesPage.showDelete = false;
                    this.filesPage.selected = [];
                    await this.loadFiles(this.filesPage.path);
                    return;
                }
            }
            this.filesPage.showDelete = false;
            this.filesPage.selected = [];
            this.showToast(this.t('msg.op_success'), 'success');
            await this.loadFiles(this.filesPage.path);
        },
        // ═══ 权限对话框(单条属性回显 + 批量 chmod 共用) ═══
        // modeToSymbolic('0644') -> '-rw-r--r--'(纯 JS 预览,不请求后端);
        // Task 15 复审 M2:正则收紧为 ^0?[0-7]{3}$ 与后端对齐(拒 1755 等 setuid 位),非法返回 null
        modeToSymbolic(mode) {
            if (!/^0?[0-7]{3}$/.test(mode || '')) return null;
            const perm = mode.slice(-3);
            const bits = [['r', 4], ['w', 2], ['x', 1]];
            let out = '-';
            for (const digit of perm) {
                const n = parseInt(digit, 10);
                for (const [ch, val] of bits) out += (n & val) ? ch : '-';
            }
            return out;
        },
        async openPermsDialog(entry) {
            if (!this.users.length) await this.loadUsers();
            if (!this.userGroups.length) await this.loadUserGroups();
            const data = await this.api('/files/stat?path=' + encodeURIComponent(this.filesPage.relJoin(entry.name)));
            // Task 15 复审 I1:api() 对非 JSON 错误响应(http.Error 纯文本)返回文本字符串(真值),
            // !data 拦不住——调用方守卫:必须是含 mode 字段的对象,否则 toast 错误文本且不开对话框
            if (!data || typeof data !== 'object' || !data.mode) {
                if (typeof data === 'string' && data.trim()) this.showToast(data.trim(), 'error');
                return;
            }
            this.filesPage.permsDialog = {
                show: true, entry, stat: data, modeInput: data.mode, recursive: false,
                ownerSel: '', groupSel: '', origOwner: data.owner, origGroup: data.group,
                confirmText: '', error: '', saving: false,
                batchMode: false, batchEntries: [], batchProgress: { active: false, done: 0, total: 0 },
            };
        },
        async openBatchPermsDialog() {
            const entries = this.filesPage.selected.slice();
            if (!entries.length) return;
            this.filesPage.permsDialog = {
                show: true, entry: null, stat: null, modeInput: '', recursive: false,
                ownerSel: '', groupSel: '', origOwner: '', origGroup: '',
                confirmText: '', error: '', saving: false,
                batchMode: true, batchEntries: entries, batchProgress: { active: false, done: 0, total: entries.length },
            };
        },
        closePermsDialog() {
            this.filesPage.permsDialog.show = false;
        },
        // type-to-confirm 禁用判断:单条目录勾 recursive、或批量模式,须输入固定确认词
        permsConfirmDisabled() {
            const d = this.filesPage.permsDialog;
            const needConfirm = d.batchMode || (d.recursive && d.entry && d.entry.type === 'dir');
            if (!needConfirm) return false;
            return d.confirmText !== this.t('files.perms_confirm_word');
        },
        async savePerms() {
            const d = this.filesPage.permsDialog;
            if (this.permsConfirmDisabled()) return;
            if (d.batchMode) {
                await this.saveBatchPerms();
                return;
            }
            // Task 15 复审 I2:mode 合法性前端闸门(非空时),不再直接吃后端 400
            if (d.modeInput && !this.modeToSymbolic(d.modeInput)) {
                d.error = this.t('files.perms_mode_invalid');
                return;
            }
            d.error = '';
            d.saving = true;
            const rel = this.filesPage.relJoin(d.entry.name);
            // Task 15 复审 M1:mode 比较归一化(去前导零),"644" 与 "0644" 等价不触发冗余请求
            const normMode = m => String(m || '').replace(/^0+/, '');
            // Task 15 复审 I3:两请求独立失败、错误各自收集,全部成功才关对话框(拍板原文语义)
            const errors = [];
            if (d.modeInput && normMode(d.modeInput) !== normMode(d.stat.mode)) {
                try {
                    await this.fileWrite('/chmod', { path: rel, mode: d.modeInput, recursive: d.recursive });
                } catch (err) {
                    errors.push('chmod: ' + String(err.message || err));
                }
            }
            const ownerChanged = d.ownerSel && d.ownerSel !== d.origOwner;
            const groupChanged = d.groupSel && d.groupSel !== d.origGroup;
            if (ownerChanged || groupChanged) {
                try {
                    await this.fileWrite('/chown', {
                        path: rel,
                        owner: ownerChanged ? d.ownerSel : '',
                        group: groupChanged ? d.groupSel : '',
                        recursive: d.recursive,
                    });
                } catch (err) {
                    errors.push('chown: ' + String(err.message || err));
                }
            }
            this.filesPage.permsDialog.saving = false;
            if (errors.length) {
                this.filesPage.permsDialog.error = errors.join(' / ');
                return;
            }
            this.filesPage.permsDialog.show = false;
            this.showToast(this.t('msg.op_success'), 'success');
            await this.loadFiles(this.filesPage.path);
        },
        async saveBatchPerms() {
            // E2E 发现:函数级 type-to-confirm 守卫(savePerms/confirmFixHomePerms 同款纵深防御,
            // 按钮 :disabled 只是 UI 层,直接调用本函数不应绕过确认词)
            if (this.permsConfirmDisabled()) return;
            const d = this.filesPage.permsDialog;
            // Task 15 复审 I2:批量必填合法 mode,避免空 mode 直发 N 条注定 400 的请求
            if (!this.modeToSymbolic(d.modeInput)) {
                d.error = this.t('files.perms_mode_invalid');
                return;
            }
            const entries = d.batchEntries;
            d.error = '';
            d.saving = true;
            this.filesPage.permsDialog.batchProgress = { active: true, done: 0, total: entries.length };
            const failures = [];
            for (const entry of entries) {
                try {
                    await this.fileWrite('/chmod', {
                        path: this.filesPage.relJoin(entry.name),
                        mode: d.modeInput,
                        recursive: d.recursive && entry.type === 'dir',
                    });
                } catch (err) {
                    failures.push({ name: entry.name, error: String(err.message || err) });
                }
                this.filesPage.permsDialog.batchProgress.done++;
            }
            this.filesPage.permsDialog.batchProgress.active = false;
            this.filesPage.permsDialog.saving = false;
            const successCount = entries.length - failures.length;
            if (failures.length) {
                this.filesPage.permsDialog.error = failures.map(f => f.name + ': ' + f.error).join('; ');
                this.showToast(this.t('files.perms_batch_summary', [successCount, failures.length]), 'error');
            } else {
                this.filesPage.permsDialog.show = false;
                this.showToast(this.t('files.perms_batch_summary', [successCount, 0]), 'success');
            }
            this.filesPage.selected = [];
            await this.loadFiles(this.filesPage.path);
        },
        // ═══ 用户页:修复 home 权限(type-to-confirm 输入用户名) ═══
        openFixHomeDialog(user) {
            this.fixHomeDialog = { show: true, username: user.username, confirmText: '', saving: false };
        },
        fixHomeConfirmDisabled() {
            return this.fixHomeDialog.confirmText !== this.fixHomeDialog.username;
        },
        async confirmFixHomePerms() {
            if (this.fixHomeConfirmDisabled()) return;
            this.fixHomeDialog.saving = true;
            try {
                const res = await fetch('/api/files/fix-home-perms', {
                    method: 'POST',
                    headers: { 'Authorization': 'Bearer ' + this.token, 'Content-Type': 'application/json' },
                    body: JSON.stringify({ username: this.fixHomeDialog.username }),
                });
                if (res.status === 401) { this.logout(); return; }
                if (!res.ok) throw new Error(await res.text());
                this.fixHomeDialog.show = false;
                this.showToast(this.t('msg.op_success'), 'success');
            } catch (err) {
                this.showToast(String(err.message || err), 'error');
            } finally {
                this.fixHomeDialog.saving = false;
            }
        },
        fmtTime(ts) {
            if (!ts) return '-';
            return new Date(ts * 1000).toLocaleString();
        },

        connProtoStyle(p) {
            window.Alpine.store('i18n').lang; // reactive dep
            const map = {
                smb: 'background:var(--primary-light);color:var(--primary)',
                ssh: 'background:var(--success-light);color:var(--success)',
                nfs: 'background:var(--card-bg-alt);color:var(--text-light)',
                ftp: 'background:var(--warn-light);color:var(--warn)',
                webdav: 'background:var(--warn-light);color:var(--warn)',
                s3: 'background:var(--warn-light);color:var(--warn)',
                web: 'background:var(--card-bg-alt);color:var(--text-secondary)'
            };
            return map[p] || 'background:var(--card-bg-alt);color:var(--text-secondary)';
        },

        fmtConnTime(iso) {
            if (!iso) return '-';
            try {
                const d = new Date(iso);
                if (isNaN(d.getTime())) return iso;
                const diff = Math.floor((Date.now() - d.getTime()) / 1000);
                if (diff < 60) return this.t('monitor.conn_just_now');
                if (diff < 3600) return this.t('monitor.conn_minutes', [Math.floor(diff / 60)]);
                if (diff < 86400) return this.t('monitor.conn_hours', [Math.floor(diff / 3600)]);
                return this.t('monitor.conn_days', [Math.floor(diff / 86400)]);
            } catch (e) { return iso; }
        },

        initMonitorRefresh() {
            if (this.monitorTimer) clearInterval(this.monitorTimer);
            this.loadMonitor();
            this.monitorTimer = setInterval(() => {
                if (this.page === 'monitor') this.loadMonitor();
            }, 180000);
        },

        async loadAlertConfig() {
            const data = await this.api('/alert-config');
            if (data && data.config) this.alertConfig = data.config;
        },

        async saveAlertConfig() {
            const params = new URLSearchParams();
            for (const [key, value] of Object.entries(this.alertConfig)) {
                params.append(key, value);
            }
            const data = await this.api('/alert-config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.save_success'), 'success');
                this.loadMonitor();
            }
        },

        // Config management
        async loadEnvConfig() {
            const data = await this.api('/config/env');
            if (data && data.config) this.envConfig = data.config;
        },

        async loadSambaShares() {
            const data = await this.api('/config/samba');
            if (data) this.sambaShares = data.shares || [];
        },

        async addSambaShare() {
            if (!this.shareForm.name || !this.shareForm.path) {
                this.showToast(this.t('msg.share_name_path_required'), 'error'); return;
            }
            const data = await this.api('/config/samba/share', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams(this.shareForm).toString()
            });
            if (data) {
                this.showToast(this.t('msg.add_success'), 'success');
                this.showAddShare = false;
                this.shareForm = { name: '', path: '', comment: '', valid_users: '', read_only: false };
                this.loadSambaShares();
            }
        },

        async deleteSambaShare(name) {
            if (!confirm(this.t('msg.del_share_confirm', [name]))) return;
            const data = await this.api(`/config/samba/share?name=${name}`, { method: 'DELETE' });
            if (data) {
                this.showToast(this.t('msg.del_success'), 'success');
                this.loadSambaShares();
            }
        },

        async loadVsftpdUsers() {
            const data = await this.api('/config/vsftpd-users');
            if (data) this.vsftpdUsers = data.users || [];
        },

        async addFtpUser() {
            if (!this.ftpUserForm.username) { this.showToast(this.t('msg.enter_username'), 'error'); return; }
            const data = await this.api('/config/vsftpd-users', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `username=${encodeURIComponent(this.ftpUserForm.username)}`
            });
            if (data) {
                this.showToast(this.t('msg.add_success'), 'success');
                this.ftpUserForm.username = '';
                this.loadVsftpdUsers();
            }
        },

        async removeFtpUser(username) {
            if (!confirm(this.t('msg.ftp_remove_confirm', [username]))) return;
            const data = await this.api(`/config/vsftpd-users?username=${username}`, { method: 'DELETE' });
            if (data) {
                this.showToast(this.t('msg.remove_success'), 'success');
                this.loadVsftpdUsers();
            }
        },

        async loadConfigFile() {
            if (!this.configFileForm.name) return;
            this.configFileForm.content = this.t('common.loading');
            const data = await this.api(`/config/file?name=${this.configFileForm.name}`);
            if (data) this.configFileForm.content = data.content || '';
        },

        async saveConfigFile() {
            const data = await this.api('/config/file', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `name=${this.configFileForm.name}&content=${encodeURIComponent(this.configFileForm.content)}`
            });
            if (data) this.showToast(this.t('msg.save_success'), 'success');
        },

        async loadEnabledServices() {
            this.enabledServices = this.t('common.loading');
            const data = await this.api('/config/services');
            if (data) this.enabledServices = data;
        },

        async toggleService(action) {
            if (!this.svcToggleForm.service) { this.showToast(this.t('msg.enter_service'), 'error'); return; }
            const data = await this.api('/config/service-toggle', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `service=${this.svcToggleForm.service}&action=${action}`
            });
            if (data) {
                this.showToast(this.t('msg.op_success'), 'success');
                this.svcToggleForm.service = '';
            }
        },

        // Disk management
        async loadDiskInfo() {
            this.diskInfo = this.t('common.loading');
            const data = await this.api('/disk/info');
            if (data) this.diskInfo = data;
        },

        async loadDiskMounts() {
            this.diskMounts = this.t('common.loading');
            const data = await this.api('/disk/mounts');
            if (data) this.diskMounts = data;
        },

        async loadDiskLVM() {
            this.diskLVM = this.t('common.loading');
            const data = await this.api('/disk/lvm');
            if (data) this.diskLVM = data;
        },

        async loadDiskIOStat() {
            this.diskIOStat = this.t('msg.testing_3s');
            const data = await this.api('/disk/iostat');
            if (data) this.diskIOStat = data;
        },

        async loadDiskSmartDetail() {
            this.diskSmartDetail = this.t('common.loading');
            const data = await this.api('/disk/smart-detail');
            if (data) this.diskSmartDetail = data;
        },

        async loadDiskPartitions() {
            this.diskPartitions = this.t('common.loading');
            const data = await this.api('/disk/partitions');
            if (data) this.diskPartitions = data;
        },

        async diskMkdir() {
            if (!this.diskMkdirForm.path) { this.showToast(this.t('msg.enter_path'), 'error'); return; }
            const data = await this.api('/disk/mkdir', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `path=${encodeURIComponent(this.diskMkdirForm.path)}`
            });
            if (data) {
                this.showToast(this.t('msg.create_success'), 'success');
                this.diskMkdirForm.path = '';
            }
        },

        async diskMount() {
            if (!this.mountForm.device || !this.mountForm.mountpoint) { this.showToast(this.t('msg.device_mount_required'), 'error'); return; }
            const data = await this.api('/disk/mount', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams(this.mountForm).toString()
            });
            if (data) {
                this.showToast(this.t('msg.mount_success'), 'success');
                this.loadDiskMounts();
            }
        },

        async diskUnmount() {
            if (!this.unmountForm.target) { this.showToast(this.t('msg.enter_mount_or_device'), 'error'); return; }
            const data = await this.api('/disk/unmount', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `target=${encodeURIComponent(this.unmountForm.target)}`
            });
            if (data) {
                this.showToast(this.t('msg.unmount_success'), 'success');
                this.unmountForm.target = '';
                this.loadDiskMounts();
            }
        },

        async diskFormat() {
            if (!this.formatForm.device) { this.showToast(this.t('msg.enter_device'), 'error'); return; }
            if (!confirm(this.t('msg.format_confirm', [this.formatForm.device, this.formatForm.fstype]))) return;
            const confirmToken = prompt(this.t('msg.confirm_type_device', [this.formatForm.device]));
            if (confirmToken !== this.formatForm.device) {
                this.showToast(this.t('msg.name_mismatch'), 'error');
                return;
            }
            const data = await this.api('/disk/format', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `device=${encodeURIComponent(this.formatForm.device)}&fstype=${this.formatForm.fstype}&confirm=yes&confirm_token=${encodeURIComponent(this.formatForm.device)}`
            });
            if (data) {
                this.showToast(this.t('msg.format_success'), 'success');
                this.formatForm.device = '';
                this.loadDiskStatus();
            }
        },

        // New: disk status overview
        async loadDiskStatus() {
            const data = await this.api('/disk/status');
            if (data) this.diskStatus = data.disks || [];
        },

        // New: quick setup
        async diskQuickSetup() {
            if (!this.quickSetupForm.device || !this.quickSetupForm.mountpoint) {
                this.showToast(this.t('msg.fill_device_mount'), 'error'); return;
            }
            if (!confirm(this.t('msg.quick_setup_confirm', [this.quickSetupForm.device, this.quickSetupForm.fstype, this.quickSetupForm.mountpoint]))) return;
            const params = new URLSearchParams({
                device: this.quickSetupForm.device,
                mountpoint: this.quickSetupForm.mountpoint,
                fstype: this.quickSetupForm.fstype,
                confirm: 'yes',
                samba: this.quickSetupForm.samba ? 'yes' : ''
            });
            const data = await this.api('/disk/quick-setup', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.config_done') + ': ' + (data.steps||[]).join(' → '), 'success');
                this.loadDiskStatus();
                this.loadFstab();
                this.quickSetupForm.device = '';
                this.quickSetupForm.mountpoint = '';
            }
        },

        // New: load fstab
        async loadFstab() {
            const data = await this.api('/disk/fstab');
            if (data !== null) this.diskFstab = data;
        },

        // New: pool status
        async loadPoolStatus() {
            const data = await this.api('/disk/pool/status');
            if (data && data.pool) this.pool = data.pool;
        },

        // New: create pool
        async createPool() {
            if (!this.poolCreateForm.devices) { this.showToast(this.t('msg.fill_disk'), 'error'); return; }
            if (!confirm(this.t('msg.create_pool_confirm', [this.poolCreateForm.devices]))) return;
            const params = new URLSearchParams({
                devices: this.poolCreateForm.devices,
                vg_name: this.poolCreateForm.vg_name,
                lv_name: this.poolCreateForm.lv_name,
                mountpoint: this.poolCreateForm.mountpoint,
                fstype: this.poolCreateForm.fstype,
                confirm: 'yes'
            });
            const data = await this.api('/disk/pool/create', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.pool_created') + ': ' + (data.steps||[]).join(' → '), 'success');
                this.loadPoolStatus();
                this.loadDiskStatus();
                this.showCreatePool = false;
            }
        },

        // New: extend pool
        async extendPool() {
            if (!this.poolExtendForm.device) { this.showToast(this.t('msg.fill_disk'), 'error'); return; }
            if (!confirm(this.t('msg.join_pool_confirm', [this.poolExtendForm.device, this.poolExtendForm.vg_name]))) return;
            const params = new URLSearchParams({
                device: this.poolExtendForm.device,
                vg_name: this.poolExtendForm.vg_name,
                confirm: 'yes'
            });
            const data = await this.api('/disk/pool/extend', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.expand_done') + ': ' + (data.steps||[]).join(' → '), 'success');
                this.loadPoolStatus();
                this.loadDiskStatus();
                this.showExtendPool = false;
                this.poolExtendForm.device = '';
            }
        },

        // Wizard: load status
        async loadWizardStatus() {
            const data = await this.api('/disk/wizard/status');
            if (data) {
                this.wizard = data;
                // Map unused_disks to available_disks for the wizard UI
                this.wizardStatus = {
                    available_disks: data.unused_disks || [],
                    pool: data.pool,
                    existing_mounts: data.existing_mounts,
                    has_storage: data.has_storage,
                    raid_options: data.raid_options
                };
                const diskData = await this.api('/disk/status');
                if (diskData && diskData.disks) {
                    let id = 0;
                    this.allDisks = diskData.disks.filter(d => d.name !== 'sr0').map(d => {
                        id++;
                        return {
                            ...d,
                            friendly: this.t('msg.disk_label') + ' ' + id,
                            type: (d.type === 'system' || (d.children && d.children.some(c => c.type === 'system'))) ? 'system' : d.type
                        };
                    });
                }
            }
        },

        selectWizardGoal(goal) {
            this.wizardGoal = goal;
            this.wizGoal = goal;
            this.wizardMode = '';
            const labels = { safety: this.t('storage.safety_goal'), capacity: this.t('storage.goal_capacity_short'), performance: this.t('storage.goal_perf_short'), balance: this.t('storage.goal_balance_short') };
            this.wizardGoalText = labels[goal] || goal;
        },

        get wizardFilteredOptions() {
            if (!this.wizGoal || !this.wizard.raid_options) return [];
            let opts = this.wizard.raid_options.filter(o => {
                if (o.goal !== this.wizGoal) return false;
                if (this.wizDisks && this.wizDisks.length > 0) {
                    if (this.wizDisks.length < o.min_disks) return false;
                    if (o.max_disks > 0 && this.wizDisks.length > o.max_disks) return false;
                }
                return true;
            });
            if (opts.length === 0) {
                opts = this.wizard.raid_options.filter(o => {
                    if (this.wizDisks && this.wizDisks.length > 0) {
                        if (this.wizDisks.length < o.min_disks) return false;
                        if (o.max_disks > 0 && this.wizDisks.length > o.max_disks) return false;
                    }
                    return true;
                });
            }
            // 按第 1 步实际所选磁盘重算可用容量（后端 usable_size 是按全部空闲盘算的）
            return opts.map(o => Object.assign({}, o, { usable_size: this.wizUsableText(o) }));
        },
        // ── 向导容量预估（按已选磁盘） ──
        wizParseSizeGB(s) {
            if (!s) return 0;
            const m = String(s).trim().match(/^([\d.]+)\s*([TGMK]?)B?$/i);
            if (!m) return 0;
            const num = parseFloat(m[1]);
            switch (m[2].toUpperCase()) {
                case 'T': return num * 1024;
                case 'G': return num;
                case 'M': return num / 1024;
                case 'K': return num / (1024 * 1024);
                default: return num;
            }
        },
        wizFormatGB(gb) {
            if (gb >= 1024) return (gb / 1024).toFixed(1) + 'T';
            if (gb >= 1) return Math.round(gb) + 'G';
            if (gb > 0) return Math.round(gb * 1024) + 'M';
            return '—';
        },
        get wizSelectedSizesGB() {
            const avail = (this.wizardStatus && this.wizardStatus.available_disks) || [];
            return (this.wizDisks || []).map(dev => {
                const d = avail.find(x => x.device === dev);
                return d ? this.wizParseSizeGB(d.size) : 0;
            });
        },
        get wizSelectedTotalText() {
            const sizes = this.wizSelectedSizesGB;
            if (!sizes.length) return '—';
            return this.wizFormatGB(sizes.reduce((a, b) => a + b, 0));
        },
        wizUsableGB(optId) {
            const sizes = this.wizSelectedSizesGB;
            if (!sizes.length) return 0;
            const n = sizes.length;
            const min = Math.min.apply(null, sizes);
            const total = sizes.reduce((a, b) => a + b, 0);
            switch (optId) {
                case 'single': return sizes[0]; // 后端只用第一块盘
                case 'raid1': return Math.min(sizes[0], sizes[1] !== undefined ? sizes[1] : sizes[0]);
                case 'raid5': return n >= 3 ? min * (n - 1) : 0;
                case 'raid6': return n >= 4 ? min * (n - 2) : 0;
                default: return total; // merge / raid0 / separate
            }
        },
        wizUsableText(opt) {
            const gb = this.wizUsableGB(opt.id);
            return gb > 0 ? this.wizFormatGB(gb) : (opt.usable_size || '—');
        },
        // Wizard: start setup from UI
        async startWizardSetup() {
            if (!this.wizMode) { this.showToast(this.t('msg.select_plan'), 'error'); return; }
            if (!this.wizDisks || this.wizDisks.length === 0) { this.showToast(this.t('msg.select_disks'), 'error'); return; }

            this.wizStep = 5;
            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = this.t('msg.create_pool_title');

            try {
                const token = this.token;
                const diskParam = (this.wizDisks && this.wizDisks.length) ? `&disks=${encodeURIComponent(this.wizDisks.join(','))}` : '';
                const resp = await fetch(`/api/disk/wizard/setup-stream?mode=${this.wizMode}&confirm=yes${diskParam}`, {
                    headers: { 'Authorization': 'Bearer ' + token }
                });
                const reader = resp.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';

                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    buffer += decoder.decode(value, { stream: true });
                    const lines = buffer.split('\n');
                    buffer = lines.pop();
                    for (const line of lines) {
                        if (line.startsWith('data: ')) {
                            try {
                                const ev = JSON.parse(line.slice(6));
                                if (ev.status === 'running') {
                                    this.progressSteps.push({ name: ev.step, status: 'running' });
                                } else if (ev.status === 'done') {
                                    const last = this.progressSteps[this.progressSteps.length - 1];
                                    if (last && last.status === 'running' && last.name === ev.step) {
                                        last.status = 'done';
                                    } else {
                                        this.progressSteps.push({ name: ev.step, status: 'done' });
                                    }
                                } else if (ev.status === 'complete') {
                                    this.progressSteps.push({ name: ev.detail || this.t('msg.done'), status: 'complete' });
                                } else if (ev.status === 'error') {
                                    this.progressSteps.push({ name: ev.step + ': ' + (ev.detail||''), status: 'error' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: this.t('msg.error') + ': ' + e.message, status: 'error' });
            }
            this.wizardLoading = false;
            setTimeout(() => { this.loadStorageOverview(); this.loadWizardStatus(); this.loadSharedFolders(); }, 2000);
        },
        // Wizard: setup (streaming with progress)
        async wizardSetup(mode) {
            if (!mode) { this.showToast(this.t('msg.select_mode'), 'error'); return; }
            const modeName = {single: this.t('msg.mode_single'), merge: this.t('msg.mode_merge'), separate: this.t('msg.mode_separate'), raid1: this.t('msg.mode_raid1')}[mode];
            if (!confirm(this.t('msg.run_mode_confirm', [modeName]))) return;

            // Show progress panel
            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = this.t('msg.storage_progress');

            try {
                const token = this.token;
                const resp = await fetch(`/api/disk/wizard/setup-stream?mode=${mode}&confirm=yes`, {
                    headers: { 'Authorization': 'Bearer ' + token }
                });
                const reader = resp.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';

                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    buffer += decoder.decode(value, { stream: true });
                    const lines = buffer.split('\n');
                    buffer = lines.pop();
                    for (const line of lines) {
                        if (line.startsWith('data: ')) {
                            try {
                                const ev = JSON.parse(line.slice(6));
                                if (ev.status === 'running') {
                                    this.progressSteps.push({ name: ev.step, status: 'running', index: ev.index, total: ev.total });
                                } else if (ev.status === 'done') {
                                    const last = this.progressSteps[this.progressSteps.length - 1];
                                    if (last && last.status === 'running' && last.name === ev.step) {
                                        last.status = 'done';
                                    } else {
                                        this.progressSteps.push({ name: ev.step, status: 'done', index: ev.index, total: ev.total });
                                    }
                                } else if (ev.status === 'complete') {
                                    this.progressSteps.push({ name: ev.detail || this.t('msg.done'), status: 'complete' });
                                } else if (ev.status === 'error') {
                                    this.progressSteps.push({ name: ev.step + ': ' + (ev.detail||''), status: 'error' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: this.t('msg.error') + ': ' + e.message, status: 'error' });
            }
            this.wizardLoading = false;
            this.progressTitle = '';
            // Auto refresh after 2s
            setTimeout(() => { this.loadStorageOverview(); this.loadWizardStatus(); this.loadSharedFolders(); }, 2000);
        },

        // Storage overview
        async loadStorageOverview() {
            const data = await this.api('/disk/overview');
            if (data && data.overview) this.storageOverview = data.overview;
        },

        scrollToWizard() {
            const el = document.getElementById('wizard-section');
            if (el) el.scrollIntoView({ behavior: 'smooth', block: 'center' });
        },

        // Maintenance operations log
        async loadOperationsLog() {
            const data = await this.api('/disk/operations?limit=20');
            if (data) this.operationsLog = data.operations || [];
        },

        // Shared folders
        async loadSharedFolders() {
            const data = await this.api('/disk/folders');
            if (data) this.sharedFolders = data.folders || [];
        },

        async createFolder() {
            if (!this.folderForm.pool || !this.folderForm.name) {
                this.showToast(this.t('msg.select_space_folder'), 'error'); return;
            }
            const params = new URLSearchParams({
                pool: this.folderForm.pool,
                name: this.folderForm.name,
                permission: this.folderForm.permission,
                valid_users: this.folderForm.valid_users.join(','),
                recycle_bin: this.folderForm.recycle_bin ? 'yes' : '',
                nfs: this.folderForm.nfs ? 'yes' : '',
                quota_gb: String(this.folderForm.quota_gb || 0)
            });
            const data = await this.api('/disk/folders/create', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.folder_created'), 'success');
                if (data.warning) {
                    setTimeout(() => this.showToast(data.warning, 'error'), 500);
                }
                this.showAddFolder = false;
                this.showUserDropdown = false;
                this.folderForm = { pool: '', name: '', permission: 'readwrite', valid_users: [], recycle_bin: false, nfs: false, quota_gb: 0 };
                this.loadSharedFolders();
                this.loadStorageOverview();
            }
        },

        async deleteFolder(f) {
            if (!confirm(this.t('msg.del_folder_confirm', [f.name, f.path]))) return;
            const data = await this.api('/disk/folders/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `path=${encodeURIComponent(f.path)}&confirm=yes`
            });
            if (data) {
                this.showToast(this.t('msg.folder_queued'), 'success');
                this.loadPendingOps();
                this.loadSharedFolders();
                this.loadStorageOverview();
            }
        },

        editFolderPermission(f) {
            this.folderPermForm = {
                name: f.name,
                path: f.path || '',
                pool: f.pool || '',
                permission: f.permission || 'readwrite',
                valid_users: f.valid_users || '',
                recycle_bin: f.recycle_bin || false
            };
            this.showFolderPerm = true;
        },

        async saveFolderPermission() {
            const params = new URLSearchParams({
                path: this.folderPermForm.path,
                permission: this.folderPermForm.permission,
                valid_users: this.folderPermForm.valid_users,
                recycle_bin: this.folderPermForm.recycle_bin ? 'yes' : 'no'
            });
            const data = await this.api('/disk/folders/permission', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.perm_matrix_updated'), 'success');
                this.showFolderPerm = false;
                this.loadSharedFolders();
                this.loadStorageOverview();
            }
        },

        // Pool extend with SSE progress
        async extendPoolStream() {
            if (!this.poolExtendForm.device) { this.showToast(this.t('msg.select_disks'), 'error'); return; }
            if (!confirm(this.t('msg.join_pool_confirm2', [this.poolExtendForm.device]))) return;

            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = this.t('msg.expand_progress');

            try {
                const params = new URLSearchParams({
                    vg_name: this.poolExtendForm.vg_name,
                    device: this.poolExtendForm.device,
                    confirm: 'yes'
                });
                const resp = await fetch(`/api/disk/pool/extend-stream?${params.toString()}`, {
                    headers: { 'Authorization': 'Bearer ' + this.token }
                });
                const reader = resp.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';
                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    buffer += decoder.decode(value, { stream: true });
                    const lines = buffer.split('\n');
                    buffer = lines.pop();
                    for (const line of lines) {
                        if (line.startsWith('data: ')) {
                            try {
                                const ev = JSON.parse(line.slice(6));
                                if (ev.status === 'running') {
                                    this.progressSteps.push({ name: ev.step, status: 'running' });
                                } else if (ev.status === 'done') {
                                    const last = this.progressSteps[this.progressSteps.length - 1];
                                    if (last && last.status === 'running' && last.name === ev.step) {
                                        last.status = 'done';
                                    } else {
                                        this.progressSteps.push({ name: ev.step, status: 'done' });
                                    }
                                } else if (ev.status === 'complete') {
                                    this.progressSteps.push({ name: ev.detail || this.t('msg.done'), status: 'complete' });
                                } else if (ev.status === 'error') {
                                    this.progressSteps.push({ name: ev.step + ': ' + (ev.detail||''), status: 'error' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: this.t('msg.error') + ': ' + e.message, status: 'error' });
            }
            this.wizardLoading = false;
            this.showExtendPool = false;
            this.progressTitle = '';
            setTimeout(() => { this.loadStorageOverview(); this.loadWizardStatus(); }, 2000);
        },

        // Open RAID expand dialog
        openRAIDExpand(pool) {
            const raidType = pool.type.toUpperCase();
            this.raidExpandForm = {
                mdDevice: pool.device,
                device: '',
                poolName: pool.display_name,
                raidType: raidType
            };
            this.showRAIDExpand = true;
        },

        // RAID expand with SSE progress
        async expandRAIDStream() {
            if (!this.raidExpandForm.device) { this.showToast(this.t('msg.select_disks'), 'error'); return; }
            const raidType = this.raidExpandForm.raidType;
            const warnMsg = raidType === 'RAID1'
                ? this.t('msg.raid_join_confirm', [this.raidExpandForm.device, this.raidExpandForm.mdDevice])
                : this.t('msg.raid_join_confirm_reshape', [this.raidExpandForm.device, this.raidExpandForm.mdDevice]);
            if (!confirm(warnMsg)) return;

            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = this.t('msg.raid_expand_progress');

            try {
                const params = new URLSearchParams({
                    md_device: this.raidExpandForm.mdDevice,
                    device: this.raidExpandForm.device,
                    confirm: 'yes'
                });
                const resp = await fetch(`/api/disk/raid/expand-stream?${params.toString()}`, {
                    headers: { 'Authorization': 'Bearer ' + this.token }
                });
                const reader = resp.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';
                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    buffer += decoder.decode(value, { stream: true });
                    const lines = buffer.split('\n');
                    buffer = lines.pop();
                    for (const line of lines) {
                        if (line.startsWith('data: ')) {
                            try {
                                const ev = JSON.parse(line.slice(6));
                                if (ev.status === 'running') {
                                    this.progressSteps.push({ name: ev.step + (ev.detail ? ': ' + ev.detail : ''), status: 'running' });
                                } else if (ev.status === 'done') {
                                    const last = this.progressSteps[this.progressSteps.length - 1];
                                    if (last && last.status === 'running') {
                                        last.status = 'done';
                                        last.name = ev.step;
                                    } else {
                                        this.progressSteps.push({ name: ev.step, status: 'done' });
                                    }
                                } else if (ev.status === 'complete') {
                                    this.progressSteps.push({ name: ev.detail || this.t('msg.done'), status: 'complete' });
                                } else if (ev.status === 'reshaping') {
                                    this.progressSteps.push({ name: ev.detail || this.t('msg.reshaping'), status: 'complete' });
                                } else if (ev.status === 'error') {
                                    this.progressSteps.push({ name: ev.step + ': ' + (ev.detail||''), status: 'error' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: this.t('msg.error') + ': ' + e.message, status: 'error' });
            }
            this.wizardLoading = false;
            this.showRAIDExpand = false;
            this.progressTitle = '';
            setTimeout(() => { this.loadStorageOverview(); this.loadWizardStatus(); }, 2000);
        },

        // Wizard: reset storage (streaming)
        async resetStorage() {
            if (!confirm(this.t('msg.reset_confirm'))) return;
            const confirmWord = prompt(this.t('msg.confirm_type_delete'));
            if (confirmWord !== 'DELETE') {
                this.showToast(this.t('msg.name_mismatch'), 'error');
                return;
            }

            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = this.t('msg.reset_progress');

            try {
                const resp = await fetch(`/api/disk/wizard/reset-stream?confirm=yes&confirm_token=DELETE`, {
                    headers: { 'Authorization': 'Bearer ' + this.token }
                });
                const reader = resp.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';
                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    buffer += decoder.decode(value, { stream: true });
                    const lines = buffer.split('\n');
                    buffer = lines.pop();
                    for (const line of lines) {
                        if (line.startsWith('data: ')) {
                            try {
                                const ev = JSON.parse(line.slice(6));
                                if (ev.status === 'running') {
                                    this.progressSteps.push({ name: ev.step, status: 'running', index: ev.index, total: ev.total });
                                } else if (ev.status === 'done') {
                                    const last = this.progressSteps[this.progressSteps.length - 1];
                                    if (last && last.status === 'running' && last.name === ev.step) {
                                        last.status = 'done';
                                    } else {
                                        this.progressSteps.push({ name: ev.step, status: 'done', index: ev.index, total: ev.total });
                                    }
                                } else if (ev.status === 'complete') {
                                    this.progressSteps.push({ name: ev.detail || this.t('msg.done'), status: 'complete' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: this.t('msg.error') + ': ' + e.message, status: 'error' });
            }
            this.wizardLoading = false;
            this.progressTitle = '';
            setTimeout(() => { this.loadStorageOverview(); this.loadWizardStatus(); this.loadSharedFolders(); }, 2000);
        },

        // ===== Pool operations =====

        // Open extend pool dialog for a specific pool
        openExtendPool(pool) {
            if (!pool) {
                this.showToast(this.t('msg.select_pool_first'), 'error');
                return;
            }
            // RAID 池走 RAID 扩容（mdadm），LVM/单盘池走 LVM 扩容
            if ((pool.type || '').toLowerCase().indexOf('raid') === 0) {
                this.openRAIDExpand(pool);
                return;
            }
            this.poolExtendForm = {
                device: '',
                vg_name: pool.name || 'vg_nas',
                lv_name: pool.volumes?.[0]?.name || 'data'
            };
            this.showExtendPool = true;
        },

        // Open replace disk dialog
        openReplaceDisk(pool) {
            this.replaceDiskForm = {
                pool_name: pool ? pool.name : '',
                md_device: pool ? pool.device : '',
                old_device: '',
                new_device: ''
            };
            this.showReplaceDisk = true;
        },

        // 替换盘：切换存储池时同步 RAID 设备与故障盘列表
        onReplacePoolChange() {
            const p = this.raidPools.find(x => x.name === this.replaceDiskForm.pool_name);
            this.replaceDiskForm.md_device = p ? p.device : '';
            this.replaceDiskForm.old_device = '';
        },

        // Execute disk replacement
        async replaceDisk() {
            const f = this.replaceDiskForm;
            if (!f.md_device || !f.old_device || !f.new_device) {
                this.showToast(this.t('msg.fill_all'), 'error'); return;
            }
            if (!confirm(this.t('msg.replace_disk_confirm', [f.md_device, f.old_device, f.new_device]))) return;

            const params = new URLSearchParams({
                md_device: f.md_device,
                old_device: f.old_device,
                new_device: f.new_device,
                confirm: 'yes'
            });
            const data = await this.api('/disk/replace', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.replace_done'), 'success');
                if (data.rebuild && data.rebuild.active) {
                    this.showToast(this.t('msg.rebuilding') + ': ' + (data.rebuild.progress || '') + '%', 'info');
                }
                this.showReplaceDisk = false;
                this.loadStorageOverview();
            }
        },

        // Trigger scrub on a pool or all pools
        async syncConfigs() {
            if (!confirm(this.t('msg.regen_confirm'))) return;
            const data = await this.api('/disk/config/sync', { method: 'POST' });
            if (data) {
                this.showToast(this.t('msg.config_sync_done'), 'success');
                const cdata = await this.api('/disk/config/check');
                if (cdata) this.configIssues = { has_issues: !!cdata.has_issues, issues: cdata.issues || [] };
            }
        },

        async loadPendingOps() {
            const data = await this.api('/disk/pending');
            if (data) this.pendingCount = data.count || 0;
        },

        async applyPending() {
            if (!confirm(this.t('msg.apply_pending_confirm'))) return;
            const data = await this.api('/disk/pending/apply', { method: 'POST' });
            if (data) {
                if (data.results) {
                    this.showToast(data.results.join('\n'), 'success');
                }
                this.pendingCount = 0;
                this.loadStorageOverview();
                this.loadSharedFolders();
            }
        },

        async discardPending() {
            if (!confirm(this.t('msg.discard_pending_confirm'))) return;
            const data = await this.api('/disk/pending/discard', { method: 'POST' });
            if (data) {
                this.showToast(this.t('msg.cleared'), 'success');
                this.pendingCount = 0;
            }
        },

        async loadOperationLogs() {
            const data = await this.api('/disk/oplogs?limit=100');
            if (data) this.operationLogs = data.logs || [];
        },

        async clearOperationLogs() {
            if (!confirm(this.t('msg.clear_logs_confirm'))) return;
            const data = await this.api('/disk/oplogs/clear', { method: 'POST' });
            if (data) {
                this.showToast(this.t('msg.logs_cleared'), 'success');
                this.operationLogs = [];
            }
        },

        async loadAuditLogs() {
            const params = new URLSearchParams({
                limit: '50',
                offset: String(this.auditLogPage * 50),
                days: String(this.auditLogFilter.days || 7)
            });
            if (this.auditLogFilter.action) params.set('action', this.auditLogFilter.action);
            const data = await this.api('/logs?' + params.toString());
            if (data) {
                this.auditLogs = data.logs || [];
                this.auditLogTotal = data.total || 0;
            }
        },

        async clearAuditLogs() {
            if (!confirm(this.t('msg.clear_all_logs_confirm'))) return;
            const data = await this.api('/logs/clear', { 
                method: 'POST', 
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: 'confirm=yes' 
            });
            if (data) {
                this.showToast(this.t('msg.logs_cleared'), 'success');
                this.auditLogs = [];
                this.auditLogTotal = 0;
            }
        },

        async scrubPool(pool) {
            const target = pool ? pool.device + ' (' + pool.display_name + ')' : this.t('msg.all_raid');
            if (!confirm(this.t('msg.scrub_confirm', [target]))) return;

            const params = new URLSearchParams({ confirm: 'yes' });
            if (pool && pool.device) {
                params.set('md_device', pool.device);
            }
            const data = await this.api('/disk/scrub', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.scrub_started'), 'success');
                this.loadOperationsLog();
            }
        },

        // Start SMART self-test on all disks
        async startSMARTScan() {
            if (!confirm(this.t('msg.smart_confirm'))) return;

            const params = new URLSearchParams({ type: 'short', confirm: 'yes' });
            const data = await this.api('/disk/smart-scan', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.smart_started'), 'success');
                this.loadOperationsLog();
            }
        },

        // Delete pool
        async deletePool(pool) {
            if (!pool) return;
            const poolName = pool.display_name || pool.name;
            if (!confirm(this.t('msg.del_pool_confirm', [poolName]))) return;

            const confirmName = prompt(this.t('msg.confirm_prompt', [poolName]));
            if (confirmName !== poolName) {
                this.showToast(this.t('msg.name_mismatch'), 'error');
                return;
            }

            const params = new URLSearchParams({
                pool_name: poolName,
                pool_type: pool.type,
                pool_device: pool.device || '',
                confirm_name: poolName,
                confirm: 'yes'
            });
            const data = await this.api('/disk/pool/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(this.t('msg.pool_deleted'), 'success');
                this.loadStorageOverview();
                this.loadWizardStatus();
                this.loadSharedFolders();
            }
        },

        // System settings — new unified page
        // 加载隐私声明与法律条款（结构化）
        async loadNotice() {
            if (this.noticeSections.length) return;
            const data = await this.api('/system/notice');
            if (data && data.sections) this.noticeSections = data.sections;
        },

        // 加载系统组件版本（系统详情页）
        async loadComponents() {
            const data = await this.api('/system/components');
            if (data && data.components) {
                this.components = { items: data.components, panel: data.panel };
            }
        },

        // 加载硬件档案（系统详情页）
        async loadHardware() {
            const data = await this.api('/hardware/profile');
            if (data && data.profile) {
                this.hardware = data;
            }
        },

        // 硬件档案平台标识翻译（后端返回稳定标识，中文兜底）
        hwProfileLabel(id, fallback) {
            const map = {
                'intel-n': 'hardware.profile_intel_n',
                'generic-x86_64': 'hardware.profile_generic_x86',
                'generic-arm64': 'hardware.profile_generic_arm64',
                'raspberry-pi': 'hardware.profile_rpi',
                'vm-intel-n': 'hardware.profile_vm',
                'vm-generic-x86_64': 'hardware.profile_vm',
                'vm-generic-arm64': 'hardware.profile_vm',
                'vm-raspberry-pi': 'hardware.profile_vm',
            };
            return map[id] ? this.t(map[id]) : (fallback || id);
        },

        // 两步验证状态
        async load2FAStatus() {
            const data = await this.api('/2fa/status');
            if (data) this.twoFA = { enabled: !!data.enabled, pending: !!data.pending, secret: this.twoFA.secret, otpauth_url: this.twoFA.otpauth_url };
        },
        async setup2FA() {
            const data = await this.api('/2fa/setup', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: '' });
            if (data) {
                this.twoFA = { enabled: false, pending: true, secret: data.secret, otpauth_url: data.otpauth_url };
            }
        },
        async enable2FA() {
            if (!this.twoFACode) { this.showToast(this.t('msg.enter_2fa_code'), 'error'); return; }
            const data = await this.api('/2fa/enable', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: `code=${encodeURIComponent(this.twoFACode)}` });
            if (data && data.enabled) {
                this.showToast(this.t('msg.2fa_enabled'), 'success');
                this.twoFACode = '';
                this.twoFA = { enabled: true, pending: false, secret: '', otpauth_url: '' };
            }
        },
        async disable2FA() {
            if (!this.twoFACode) { this.showToast(this.t('msg.enter_2fa_code'), 'error'); return; }
            const data = await this.api('/2fa/disable', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: `code=${encodeURIComponent(this.twoFACode)}` });
            if (data && !data.enabled) {
                this.showToast(this.t('msg.2fa_disabled'), 'success');
                this.twoFACode = '';
                this.twoFA = { enabled: false, pending: false, secret: '', otpauth_url: '' };
            }
        },

        // 可导入的既有存储池（换机恢复）
        async loadImportablePools() {
            const data = await this.api('/disk/import');
            if (data) this.importablePools = data.pools || [];
        },
        async importPool(pool) {
            if (!pool || !pool.vg_name) return;
            if (!confirm(this.t('msg.import_pool_confirm', [pool.vg_name]))) return;
            const data = await this.api('/disk/import/pool', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `vg_name=${encodeURIComponent(pool.vg_name)}&confirm=yes`
            });
            if (data) {
                this.showToast(this.t('msg.import_pool_success'), 'success');
                this.loadImportablePools();
                this.loadStorageOverview();
                this.loadWizardStatus();
                this.loadSharedFolders();
            }
        },

        async loadSystemOverview() {
            const data = await this.api('/system/overview');
            if (data) {
                this.sysSettings.hostname = data.hostname || '';
                this.sysSettings.network = data.network || {};
                this.sysSettings.time = data.time || {};
                this.sysSettings.timezone = data.time?.timezone || 'Asia/Shanghai';
                this.sysSettings.ssh = data.ssh || {};
                this.sysSettings.sysctl = data.sysctl || [];
                this.sysSettings.updates = data.updates || {};
                this.sysSettings.services = data.services || [];
            }
            // Also load HTTPS status
            this.loadHTTPSStatus();
        },

        async saveHostname() {
            if (!this.sysSettings.hostname) {
                this.showToast(this.t('msg.enter_hostname'), 'error');
                return;
            }
            const data = await this.api('/system/hostname', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `hostname=${encodeURIComponent(this.sysSettings.hostname)}`
            });
            if (data) this.showToast(this.t('msg.modify_success'), 'success');
        },

        async saveTimezone() {
            const data = await this.api('/system/timezone', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `timezone=${encodeURIComponent(this.sysSettings.timezone)}`
            });
            if (data) this.showToast(this.t('msg.timezone_updated'), 'success');
        },

        async saveSSHConfig() {
            const ssh = this.sysSettings.ssh;
            if (!confirm(this.t('msg.ssh_confirm', [ssh.port, ssh.permit_root_login ? this.t('msg.allow') : this.t('msg.deny'), ssh.password_auth ? this.t('msg.allow') : this.t('msg.deny')]))) return;
            const params = new URLSearchParams({
                port: String(ssh.port),
                permit_root_login: ssh.permit_root_login ? 'yes' : 'no',
                password_auth: ssh.password_auth ? 'yes' : 'no',
                max_auth_tries: String(ssh.max_auth_tries || 6)
            });
            const data = await this.api('/system/ssh-config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) this.showToast(this.t('msg.ssh_updated'), 'success');
        },

        async saveSysctl(param) {
            const data = await this.api('/system/sysctl', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `key=${encodeURIComponent(param.key)}&value=${encodeURIComponent(param.value)}`
            });
            if (data) this.showToast(this.t('msg.saved'), 'success');
        },

        async runUpdate(action) {
            const msg = action === 'update' ? this.t('msg.refreshing_sources') : this.t('msg.updating_system');
            this.showToast(msg, 'info');
            const data = await this.api('/system/updates', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `action=${action}`
            });
            if (data) {
                this.showToast(this.t('msg.done'), 'success');
                this.loadSystemOverview();
            }
        },

        async serviceAction(name, action) {
            const actionText = { start: this.t('common.start'), stop: this.t('common.stop'), restart: this.t('common.restart'), enable: this.t('msg.enable_autostart'), disable: this.t('msg.disable_autostart') };
            const data = await this.api('/system/services', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `name=${encodeURIComponent(name)}&action=${action}`
            });
            if (data) {
                this.showToast(this.t('msg.x_success', [actionText[action]]), 'success');
                this.loadSystemOverview();
            }
        },

        async resetSystem() {
            if (!confirm(this.t('msg.factory_reset_confirm'))) return;
            if (!confirm(this.t('msg.factory_reset_final'))) return;
            const confirmWord = prompt(this.t('msg.confirm_type_delete'));
            if (confirmWord !== 'DELETE') {
                this.showToast(this.t('msg.name_mismatch'), 'error');
                return;
            }
            this.resetMsg = this.t('msg.resetting_wait');
            const data = await this.api('/system/reset', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: 'confirm=yes&confirm_token=DELETE'
            });
            if (data) {
                if (data.error) {
                    this.showToast(data.error, 'error');
                    this.resetMsg = this.t('msg.error') + ': ' + data.error;
                } else if (data.status === 'running') {
                    this.showToast(this.t('msg.reset_bg_started'), 'success');
                    this.resetMsg = this.t('msg.resetting');
                    // 轮询等待完成
                    await this.waitForReset();
                } else {
                    this.showToast(this.t('msg.restore_done'), 'success');
                    this.resetMsg = this.t('msg.restore_done');
                    setTimeout(() => { this.loadSystemOverview(); this.resetMsg = ''; }, 3000);
                }
            }
        },

        async waitForReset() {
            for (let i = 0; i < 60; i++) {
                await new Promise(r => setTimeout(r, 2000));
                const ov = await this.api('/disk/overview');
                if (!ov) continue;
                const d = ov.overview || ov;
                // 重置完成标志：无存储池，有可用磁盘
                if (!d.pools || d.pools.length === 0) {
                    this.showToast(this.t('msg.reset_done'), 'success');
                    this.resetMsg = this.t('msg.reset_done');
                    setTimeout(() => { location.reload(); }, 2000);
                    return;
                }
                this.resetMsg = this.t('msg.reset_progress_pct') + ' (' + (i + 1) * 2 + 's)';
            }
            this.resetMsg = this.t('msg.reset_timeout');
            this.showToast(this.t('msg.reset_may_still_run'), 'error');
        },

        // ═══ HTTPS 证书管理 ═══
        async loadHTTPSStatus() {
            const data = await this.api('/system/https');
            if (data) this.httpsStatus = data;
        },

        async generateCert() {
            const domain = this.httpsDomain || '';
            const data = await this.api('/system/https/generate', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: domain ? `domain=${encodeURIComponent(domain)}` : ''
            });
            if (data) {
                this.showToast(this.t('msg.cert_generated'), 'success');
                this.httpsStatus = data.status;
            }
        },

        async uploadCert() {
            if (!this.httpsCustomDomain || !this.httpsCustomCert || !this.httpsCustomKey) {
                this.showToast(this.t('msg.fill_cert'), 'error');
                return;
            }
            const body = `domain=${encodeURIComponent(this.httpsCustomDomain)}&cert=${encodeURIComponent(this.httpsCustomCert)}&key=${encodeURIComponent(this.httpsCustomKey)}`;
            const data = await this.api('/system/https/upload', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            if (data) {
                this.showToast(this.t('msg.cert_uploaded'), 'success');
                this.httpsStatus = data.status;
                this.httpsCustomDomain = '';
                this.httpsCustomCert = '';
                this.httpsCustomKey = '';
            }
        },

        async applyCert() {
            if (!confirm(this.t('msg.apply_cert_confirm'))) return;
            this.showToast(this.t('msg.applying_cert'), 'info');
            const data = await this.api('/system/https/apply', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' }
            });
            if (data) {
                this.showToast(this.t('msg.cert_applied'), 'success');
                this.httpsStatus = data.status;
            }
        },

        async removeCert() {
            if (!confirm(this.t('msg.remove_cert_confirm'))) return;
            const data = await this.api('/system/https/remove', {
                method: 'DELETE'
            });
            if (data) {
                this.showToast(this.t('msg.cert_removed'), 'success');
                this.httpsStatus = data.status;
            }
        },

        // Backup management
        async loadBackups() {
            const data = await this.api('/backup/list');
            if (data) this.backups = data.backups || [];
        },

        async createBackup() {
            this.backupLoading = true;
            const data = await this.api('/backup/create', { method: 'POST' });
            if (data) {
                this.showToast(this.t('msg.backup_created'), 'success');
                this.loadBackups();
            }
            this.backupLoading = false;
        },

        async runDataBackup() {
            if (!this.dataBackupTarget) { this.showToast(this.t('msg.enter_backup_target'), 'error'); return; }
            this.dataBackupLoading = true;
            const data = await this.api('/backup/data', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `target=${encodeURIComponent(this.dataBackupTarget)}`
            });
            if (data) {
                this.showToast(this.t('msg.data_backup_done'), 'success');
            }
            this.dataBackupLoading = false;
        },

        // ═══ 凭证保险箱 v2（零知识：PBKDF2+AES-256-GCM 全部在浏览器端，服务器只存密文）═══

        vaultHexToBytes(hex) {
            const out = new Uint8Array(hex.length / 2);
            for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.substr(i * 2, 2), 16);
            return out;
        },

        // 直接 fetch（不走 api() helper）：解锁失败时需要读取 attempts_left / locked_remaining_seconds 等非 200 响应体字段
        async vaultFetch(path, options = {}) {
            const opts = { ...options, headers: { 'Authorization': 'Bearer ' + this.token, ...(options.headers || {}) } };
            let res;
            try {
                res = await fetch('/api/vault' + path, opts);
            } catch (e) {
                // 审查 M5：断网/DNS 失败不再产生 unhandled rejection，调用方按 !ok 处理
                return { ok: false, status: 0, data: null };
            }
            let data = null;
            try { data = await res.json(); } catch (e) {}
            // 401 分两种：JSON 业务错误（如 unlock 口令错误，带 error 字段）原样返回；
            // 纯文本 401 是 AuthMiddleware 会话过期 → 登出。此前把 unlock 的口令错误
            // 401 也当会话过期，输错一次保险箱口令就被踢回登录页（E2E 抓出）。
            if (res.status === 401 && !(data && typeof data.error === 'string')) {
                this.logout();
                return { ok: false, status: 401, data: null };
            }
            return { ok: res.ok, status: res.status, data };
        },

        async vaultOnEnter() {
            this.resetVaultIdleTimer();
            await this.vaultCheckStatus();
        },

        async vaultCheckStatus() {
            const { ok, data } = await this.vaultFetch('/status');
            if (!ok || !data) return;
            this.vaultItemCount = data.item_count || 0;
            this.vaultLegacyCount = data.legacy_count || 0;
            if (!data.setup) {
                this.vaultState = 'uninitialized';
                return;
            }
            if (data.locked_until) {
                this.vaultEnterLockedCountdown(data.locked_until);
            } else if (this.vaultState !== 'unlocked') {
                this.vaultState = 'locked';
            }
            this._vaultSalt = data.salt;
            this._vaultIterations = data.iterations;
        },

        vaultEnterLockedCountdown(lockedUntilISO) {
            this.vaultState = 'locked';
            const until = new Date(lockedUntilISO).getTime();
            if (this.vaultLockedTimer) clearInterval(this.vaultLockedTimer);
            const tick = () => {
                const remain = Math.max(0, Math.round((until - Date.now()) / 1000));
                this.vaultLockedRemaining = remain;
                if (remain <= 0 && this.vaultLockedTimer) { clearInterval(this.vaultLockedTimer); this.vaultLockedTimer = null; }
            };
            tick();
            this.vaultLockedTimer = setInterval(tick, 1000);
        },

        async vaultSetup() {
            if (this.vaultSetupForm.passphrase.length < 12) { this.showToast(this.t('vault.passphrase_too_short'), 'error'); return; }
            if (this.vaultSetupForm.passphrase !== this.vaultSetupForm.confirm) { this.showToast(this.t('vault.passphrase_mismatch'), 'error'); return; }
            this.vaultLoading = true;
            try {
                const iterations = 600000;
                const saltHex = window.ZkVault.randomHex(16);
                const saltBytes = this.vaultHexToBytes(saltHex);
                const key = await window.ZkVault.deriveKey(this.vaultSetupForm.passphrase, saltBytes, iterations);
                const verifyHash = await window.ZkVault.verificationHash(this.vaultSetupForm.passphrase, saltBytes, iterations);
                const { ok, data } = await this.vaultFetch('/setup', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: `salt=${saltHex}&verify_hash=${verifyHash}&iterations=${iterations}`
                });
                if (!ok) { this.showToast((data && data.error) || this.t('msg.network_error'), 'error'); return; }
                this.vaultMasterKey = key;
                this._vaultSalt = saltHex;
                this._vaultIterations = iterations;
                this.vaultState = 'unlocked';
                this.vaultSetupForm = { passphrase: '', confirm: '' };
                this.resetVaultIdleTimer();
                await this.loadVaultItems();
                this.showToast(this.t('vault.setup_done'), 'success');
            } finally {
                this.vaultLoading = false;
            }
        },

        async vaultUnlock() {
            if (!this._vaultSalt) await this.vaultCheckStatus();
            if (!this._vaultSalt) { this.showToast(this.t('vault.not_setup'), 'error'); return; }
            this.vaultLoading = true;
            this.vaultUnlockError = '';
            try {
                const saltBytes = this.vaultHexToBytes(this._vaultSalt);
                const key = await window.ZkVault.deriveKey(this.vaultUnlockForm.passphrase, saltBytes, this._vaultIterations);
                const verifyHash = await window.ZkVault.verificationHash(this.vaultUnlockForm.passphrase, saltBytes, this._vaultIterations);
                const { ok, status, data } = await this.vaultFetch('/unlock', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: `verify_hash=${verifyHash}`
                });
                if (!ok) {
                    if (status === 423) {
                        this.vaultEnterLockedCountdown(new Date(Date.now() + (data.locked_remaining_seconds || 0) * 1000).toISOString());
                        this.vaultUnlockError = (data && data.error) || this.t('vault.locked');
                    } else {
                        this.vaultAttemptsLeft = data && typeof data.attempts_left === 'number' ? data.attempts_left : null;
                        this.vaultUnlockError = (data && data.error) || this.t('vault.unlock_failed');
                    }
                    return;
                }
                this.vaultMasterKey = key;
                this.vaultAttemptsLeft = null;
                this.vaultUnlockForm.passphrase = '';
                this.vaultState = 'unlocked';
                this.resetVaultIdleTimer();
                await this.loadVaultItems();
            } finally {
                this.vaultLoading = false;
            }
        },

        vaultLock() {
            this.vaultMasterKey = null;
            this.vaultItems = [];
            this.vaultState = this.vaultState === 'uninitialized' ? 'uninitialized' : 'locked';
            this.vaultRevealId = null;
            if (this.vaultRevealTimer) { clearTimeout(this.vaultRevealTimer); this.vaultRevealTimer = null; }
            if (this.vaultIdleTimer) { clearTimeout(this.vaultIdleTimer); this.vaultIdleTimer = null; }
            // 审查 M4：锁定倒计时 interval 一并清理，离开页面后不再空转 30 分钟
            if (this.vaultLockedTimer) { clearInterval(this.vaultLockedTimer); this.vaultLockedTimer = null; }
            this.showVaultForm = false;
            this.showChangePassphrase = false;
            this.showLegacyMigration = false;
        },

        resetVaultIdleTimer() {
            if (this.vaultIdleTimer) clearTimeout(this.vaultIdleTimer);
            if (this.vaultState !== 'unlocked') return;
            this.vaultIdleTimer = setTimeout(() => {
                if (this.vaultState === 'unlocked') {
                    this.vaultLock();
                    this.showToast(this.t('vault.auto_locked'), 'info');
                }
            }, 15 * 60 * 1000);
        },

        async loadVaultItems() {
            const { ok, data } = await this.vaultFetch('/items');
            if (!ok || !data) return;
            const key = this.vaultMasterKey;
            const items = [];
            for (const raw of (data.items || [])) {
                let secret = '', notes = '';
                try {
                    const plain = await window.ZkVault.decryptJSON(key, raw.encrypted);
                    secret = plain.secret || plain.password || '';
                    notes = plain.notes || '';
                } catch (e) {
                    secret = '';
                    notes = this.t('vault.decrypt_failed');
                }
                items.push({ ...raw, secret, notes, _encrypted: raw.encrypted });
            }
            this.vaultItems = items;
            this.vaultItemCount = items.length;
        },

        vaultOpenForm(item) {
            this.resetVaultIdleTimer();
            if (item) {
                this.vaultForm = { id: item.id, name: item.name, category: item.category, url: item.url, username: item.username, secret: item.secret, notes: item.notes };
            } else {
                this.vaultForm = { id: null, name: '', category: '', url: '', username: '', secret: '', notes: '' };
            }
            this.showVaultForm = true;
        },

        async saveVaultItem() {
            this.resetVaultIdleTimer();
            if (!this.vaultForm.name || !this.vaultForm.secret) { this.showToast(this.t('msg.fill_required'), 'error'); return; }
            const encrypted = await window.ZkVault.encryptJSON(this.vaultMasterKey, { secret: this.vaultForm.secret, notes: this.vaultForm.notes || '' });
            const notesPreview = (this.vaultForm.notes || '').slice(0, 40);
            const body = `name=${encodeURIComponent(this.vaultForm.name)}&category=${encodeURIComponent(this.vaultForm.category || '')}` +
                `&url=${encodeURIComponent(this.vaultForm.url || '')}&username=${encodeURIComponent(this.vaultForm.username || '')}` +
                `&notes_preview=${encodeURIComponent(notesPreview)}&encrypted=${encodeURIComponent(encrypted)}`;
            const path = this.vaultForm.id ? '/update' : '/create';
            const extraBody = this.vaultForm.id ? `&id=${this.vaultForm.id}` : '';
            const { ok, data } = await this.vaultFetch(path, {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body + extraBody
            });
            if (!ok) { this.showToast((data && data.error) || this.t('msg.network_error'), 'error'); return; }
            this.showToast(this.t('vault.saved'), 'success');
            this.showVaultForm = false;
            await this.loadVaultItems();
        },

        async deleteVaultItem(v) {
            this.resetVaultIdleTimer();
            if (!confirm(this.t('msg.vault_delete_confirm', [v.name]))) return;
            const { ok, data } = await this.vaultFetch('/delete', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: `id=${v.id}` });
            if (!ok) { this.showToast((data && data.error) || this.t('msg.network_error'), 'error'); return; }
            this.showToast(this.t('msg.vault_deleted'), 'success');
            await this.loadVaultItems();
        },

        // 查看明文：15 秒后自动重新遮罩
        vaultReveal(v) {
            this.resetVaultIdleTimer();
            this.vaultRevealId = v.id;
            if (this.vaultRevealTimer) clearTimeout(this.vaultRevealTimer);
            this.vaultRevealTimer = setTimeout(() => { this.vaultRevealId = null; }, 15000);
        },

        // 复制到剪贴板：30 秒后自动清空剪贴板（尽力而为，不保证所有浏览器都支持）
        async vaultCopy(v) {
            this.resetVaultIdleTimer();
            try {
                await navigator.clipboard.writeText(v.secret);
                this.showToast(this.t('msg.vault_copied'), 'success');
                setTimeout(async () => {
                    try {
                        const cur = await navigator.clipboard.readText();
                        if (cur === v.secret) await navigator.clipboard.writeText('');
                    } catch (e) { /* 读取剪贴板权限不足时静默忽略 */ }
                }, 30000);
            } catch (e) {
                this.showToast(this.t('msg.network_error'), 'error');
            }
        },

        openChangePassphrase() {
            this.resetVaultIdleTimer();
            this.changePassphraseForm = { oldPassphrase: '', newPassphrase: '', confirm: '' };
            this.showChangePassphrase = true;
        },

        async submitChangePassphrase() {
            this.resetVaultIdleTimer();
            const f = this.changePassphraseForm;
            if (f.newPassphrase.length < 12) { this.showToast(this.t('vault.passphrase_too_short'), 'error'); return; }
            if (f.newPassphrase !== f.confirm) { this.showToast(this.t('vault.passphrase_mismatch'), 'error'); return; }
            this.changePassphraseLoading = true;
            try {
                const oldSaltBytes = this.vaultHexToBytes(this._vaultSalt);
                const oldKey = await window.ZkVault.deriveKey(f.oldPassphrase, oldSaltBytes, this._vaultIterations);
                const oldVerify = await window.ZkVault.verificationHash(f.oldPassphrase, oldSaltBytes, this._vaultIterations);
                // 用当前内存里的 masterKey 校验旧口令是否正确：比对派生出的 verify hash 与服务器记录是否一致
                // （服务端 change-passphrase 接口会在事务里用新 verify_hash 覆盖，因此这里需要先确认 oldKey == vaultMasterKey）
                const curVerify = await window.ZkVault.sha256Hex(this.vaultMasterKey);
                const checkVerify = await window.ZkVault.sha256Hex(oldKey);
                if (curVerify !== checkVerify) { this.showToast(this.t('vault.old_passphrase_wrong'), 'error'); return; }
                void oldVerify;

                const newIterations = this._vaultIterations;
                const newSaltHex = window.ZkVault.randomHex(16);
                const newSaltBytes = this.vaultHexToBytes(newSaltHex);
                const newKey = await window.ZkVault.deriveKey(f.newPassphrase, newSaltBytes, newIterations);
                const newVerify = await window.ZkVault.verificationHash(f.newPassphrase, newSaltBytes, newIterations);

                const reencrypted = [];
                // 实时拉取全部条目并用旧钥解密（审查 I4）：不用内存缓存，避免其它设备新增
                // 条目漏重加密后永久无法解密；任一条解密失败 → 中止，绝不回写空内容毁数据
                const { ok: iok, data: idata } = await this.vaultFetch('/items');
                if (!iok || !idata) { this.showToast(this.t('msg.network_error'), 'error'); return; }
                for (const item of (idata.items || [])) {
                    let obj;
                    try {
                        obj = await window.ZkVault.decryptJSON(oldKey, item.encrypted);
                    } catch (e) {
                        this.showToast(this.t('vault.reencrypt_abort') + ` (#${item.id} ${item.name})`, 'error');
                        return;
                    }
                    const encrypted = await window.ZkVault.encryptJSON(newKey, obj);
                    reencrypted.push({ id: item.id, encrypted });
                }

                const { ok, data } = await this.vaultFetch('/change-passphrase', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: `salt=${newSaltHex}&verify_hash=${newVerify}&iterations=${newIterations}&items=${encodeURIComponent(JSON.stringify(reencrypted))}`
                });
                if (!ok) { this.showToast((data && data.error) || this.t('msg.network_error'), 'error'); return; }

                this.vaultMasterKey = newKey;
                this._vaultSalt = newSaltHex;
                this.showChangePassphrase = false;
                this.showToast(this.t('vault.passphrase_changed'), 'success');
                await this.loadVaultItems();
            } finally {
                this.changePassphraseLoading = false;
            }
        },

        // 旧版（服务端加密）保险箱迁移：逐条取明文→用新保险箱密钥重新加密→写入新表，最后清空旧表
        async openLegacyMigration() {
            this.resetVaultIdleTimer();
            const { ok, data } = await this.vaultFetch('/legacy');
            if (!ok || !data) return;
            this.vaultLegacyItems = data.items || [];
            this.showLegacyMigration = true;
        },

        async migrateLegacyItems() {
            this.resetVaultIdleTimer();
            this.legacyMigrating = true;
            try {
                // 失败即停（审查 C1）：任何一条 reveal/create 失败立即中断且绝不 purge，
                // 旧表数据原样保留可重试——此前 continue+无条件 purge 会永久丢失失败条目的明文
                for (const legacy of this.vaultLegacyItems) {
                    const { ok, data } = await this.vaultFetch('/legacy/reveal', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: `id=${legacy.id}` });
                    if (!ok || !data || typeof data.secret !== 'string') {
                        this.showToast(this.t('vault.migration_failed') + ` (#${legacy.id} ${legacy.name})`, 'error');
                        return;
                    }
                    const encrypted = await window.ZkVault.encryptJSON(this.vaultMasterKey, { secret: data.secret, notes: legacy.note || '' });
                    const created = await this.vaultFetch('/create', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                        body: `name=${encodeURIComponent(legacy.name)}&category=${encodeURIComponent(legacy.category || '')}` +
                            `&url=&username=&notes_preview=${encodeURIComponent((legacy.note || '').slice(0, 40))}&encrypted=${encodeURIComponent(encrypted)}`
                    });
                    if (!created.ok) {
                        this.showToast(this.t('vault.migration_failed') + ` (#${legacy.id} ${legacy.name})`, 'error');
                        return;
                    }
                }
                const { ok: pok, data: pdata } = await this.vaultFetch('/legacy/purge', { method: 'POST' });
                if (!pok) { this.showToast((pdata && pdata.error) || this.t('msg.network_error'), 'error'); return; }
                this.showLegacyMigration = false;
                this.vaultLegacyItems = [];
                this.showToast(this.t('vault.migration_done'), 'success');
                await this.loadVaultItems();
                await this.vaultCheckStatus();
            } finally {
                this.legacyMigrating = false;
            }
        },

        // ═══ 密码生成器（独立页面，不依赖保险箱解锁，纯浏览器端 crypto.getRandomValues）═══

        pwgenCharPool() {
            const upper = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ';
            const lower = 'abcdefghijklmnopqrstuvwxyz';
            const digits = '0123456789';
            const symbols = '!@#$%^&*()-_=+[]{}<>?';
            const groups = [];
            if (this.pwgen.upper) groups.push(upper);
            if (this.pwgen.lower) groups.push(lower);
            if (this.pwgen.digits) groups.push(digits);
            if (this.pwgen.symbols) groups.push(symbols);
            return groups;
        },

        pwgenOnEnter() {
            // 独立页面，无需加载任何服务端数据
        },

        pwgenGenerate() {
            const groups = this.pwgenCharPool();
            if (!groups.length) { this.showToast(this.t('pwgen.need_one_type'), 'error'); return; }
            const length = Math.min(64, Math.max(8, this.pwgen.length || 16));
            const all = groups.join('');
            // rejection sampling（审查 I2）：丢弃超出 max 整数倍上界的采样，消除模偏差
            const randInt = (max) => {
                if (max <= 1) return 0;
                const limit = Math.floor(0x100000000 / max) * max;
                const buf = new Uint32Array(1);
                let x;
                do { crypto.getRandomValues(buf); x = buf[0]; } while (x >= limit);
                return x % max;
            };
            const chars = [];
            // 保证每类字符至少出现一次
            for (const g of groups) chars.push(g[randInt(g.length)]);
            while (chars.length < length) chars.push(all[randInt(all.length)]);
            // Fisher-Yates 洗牌
            for (let i = chars.length - 1; i > 0; i--) {
                const j = randInt(i + 1);
                [chars[i], chars[j]] = [chars[j], chars[i]];
            }
            const value = chars.slice(0, length).join('');
            this.pwgen.value = value;
            this.pwgen.strength = this.pwgenStrength(value);
            this.pwgenHistory.unshift({ value, time: new Date().toLocaleTimeString() });
            if (this.pwgenHistory.length > 20) this.pwgenHistory.pop();
        },

        // 熵估算（审查 M2）：bits = len × log2(poolSize)
        passwordEntropyBits(len, poolSize) {
            if (!len || poolSize <= 1) return 0;
            return len * (Math.log(poolSize) / Math.LN2);
        },

        // 六档强度：0 很弱 - 5 极强（<40 / <60 / <80 / <100 / <128 / ≥128 bits）
        pwgenStrength(value) {
            if (!value) return 0;
            let pool = 0;
            if (/[A-Z]/.test(value)) pool += 26;
            if (/[a-z]/.test(value)) pool += 26;
            if (/[0-9]/.test(value)) pool += 10;
            if (/[^A-Za-z0-9]/.test(value)) pool += 23;
            const bits = this.passwordEntropyBits(value.length, pool);
            if (bits < 40) return 0;
            if (bits < 60) return 1;
            if (bits < 80) return 2;
            if (bits < 100) return 3;
            if (bits < 128) return 4;
            return 5;
        },

        pwgenStrengthLabel(level) {
            const keys = ['pwgen.strength_0', 'pwgen.strength_1', 'pwgen.strength_2', 'pwgen.strength_3', 'pwgen.strength_4', 'pwgen.strength_5'];
            return this.t(keys[level] || keys[0]);
        },

        async pwgenCopy(value) {
            try {
                await navigator.clipboard.writeText(value);
                this.showToast(this.t('msg.vault_copied'), 'success');
            } catch (e) {
                this.showToast(this.t('msg.network_error'), 'error');
            }
        },

        // ═══ AI 助手：设置/开关/导航入口 ═══

        // GET /api/ai/status 是唯一免 enabled 守卫的端点，驱动导航入口显隐；
        // 应用启动（已登录）与每次进 ai 页都会调用。
        async loadAIStatus() {
            const data = await this.api('/ai/status');
            if (data && typeof data === 'object' && data.enabled !== undefined) {
                this.aiStatus = data;
                // 配置表单首次回显：仅在已启用（GET /api/ai/config 有 enabled 守卫）时拉取，
                // 未启用时表单保持用户当前输入，不强行清空。
                if (data.enabled) this.loadAIConfig();
            }
        },

        // GET /api/ai/config 需要功能已开启（requireEnabled）；未开启时不会被调用到
        // （loadAIStatus 已做分支），这里只处理已启用场景的回显。
        async loadAIConfig() {
            const data = await this.api('/ai/config');
            if (data && typeof data === 'object' && data.base_url !== undefined) {
                this.aiConfigForm = {
                    preset: data.preset || 'deepseek',
                    base_url: data.base_url || '',
                    api_key: '',
                    model: data.model || ''
                };
            }
        },

        // 预设联动：deepseek 填云端端点，ollama 填本地端点并禁用/清空 key 输入。
        applyAIPreset(preset) {
            if (preset === 'ollama') {
                this.aiConfigForm.base_url = 'http://localhost:11434/v1';
                this.aiConfigForm.model = 'qwen3:32b';
                this.aiConfigForm.api_key = '';
            } else if (preset === 'deepseek') {
                this.aiConfigForm.base_url = 'https://api.deepseek.com/v1';
                this.aiConfigForm.model = 'deepseek-chat';
            }
        },

        // POST /api/ai/config：保存端点/key/model。
        // 注意：该端点同样挂了 requireEnabled——功能未开启时保存会收到 403，
        // 这是后端当前的既有行为（T1/T2 遗留），不在本轮前端范围内变更；
        // api() 已经会把 403 的 error 文案 toast 出来，这里不用额外特殊处理。
        async saveAIConfig() {
            const data = await this.api('/ai/config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(this.aiConfigForm)
            });
            if (data && typeof data === 'object' && data.base_url !== undefined) {
                this.showToast(this.t('ai.save_ok'), 'success');
                this.aiConfigForm.api_key = '';
            }
        },

        // POST /api/ai/test-connection：无需请求体，测的是服务端已保存的配置
        // （不是表单里尚未保存的值）——先保存再测试才有意义，这里先保存一次。
        async testAIConnection() {
            this.aiTesting = true;
            try {
                await this.saveAIConfig();
                const data = await this.api('/ai/test-connection', { method: 'POST' });
                if (data && typeof data === 'object' && data.ok) {
                    this.showToast(this.t('ai.test_ok'), 'success');
                    await this.loadAIStatus();
                } else if (typeof data === 'string' && data.trim()) {
                    // 后端改 JSON content-type 后 api() 会自动 toast 并返回 null，
                    // 这个分支只是 text/plain 兜底（不会和 api() 的 toast 双弹）。
                    let msg = data;
                    try { const j = JSON.parse(data); if (j && j.error) msg = j.error; } catch (e) {}
                    this.showToast(msg, 'error');
                }
            } finally {
                this.aiTesting = false;
            }
        },

        // POST /api/ai/toggle：开关本身不受 enabled 守卫限制。
        async toggleAI(enabled) {
            const data = await this.api('/ai/toggle', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ enabled })
            });
            if (data && typeof data === 'object' && data.enabled !== undefined) {
                this.aiStatus.enabled = data.enabled;
                if (!data.enabled) this.showToast(this.t('ai.turn_off'), 'success');
                else { this.showToast(this.t('ai.enabled'), 'success'); this.loadAIStatus(); }
                // 关断/开启 AI 属 triggers/notifications 的运行态守卫来源，通知中心
                // 跟随 enabled 显隐与轮询启停（关断零足迹的 UI 表达，硬约束 1）。
                this.syncNotifPolling();
            }
            // 失败（如未配置/未验证）时 api() 已经把后端中文原因 toast 出来。
        },

        // ═══ AI 助手：聊天页 SSE 消费 + 会话列表 ═══

        async loadAIConversations() {
            const data = await this.api('/ai/conversations');
            if (data && typeof data === 'object' && data.conversations !== undefined) {
                this.aiConversations = data.conversations || [];
            }
        },

        newAIConversation() {
            this.aiConversationId = '';
            this.aiMessages = [];
            this.aiInput = '';
        },

        async loadAIConversation(convID) {
            const data = await this.api('/ai/conversations/' + encodeURIComponent(convID));
            if (data && typeof data === 'object' && data.messages !== undefined) {
                this.aiConversationId = convID;
                // 落库消息只有 role/content（工具轨迹/pending_action 是会话内即时事件产物，
                // 历史回放不还原这些临时 UI 态——与 role=tool 的内部回填消息一起过滤掉，
                // 只展示用户可见的 user/assistant 对话）。
                this.aiMessages = (data.messages || [])
                    .filter(m => m.role === 'user' || m.role === 'assistant')
                    .map(m => ({ role: m.role, content: m.content, toolTrace: [] }));
            }
        },

        // POST /api/ai/chat（Accept: text/event-stream），对齐 resetStorage() 的
        // fetch + getReader SSE 消费模式。delta 增量拼接进最后一条 assistant 消息；
        // tool_call/tool_result 渲染为该消息内的折叠小条目；pending_action 本轮只
        // 渲染只读信息卡（ai.pending_hint 占位文案，确认交互 R5 接入）。
        async sendAIMessage() {
            const message = this.aiInput.trim();
            if (!message || this.aiThinking) return;
            this.aiInput = '';
            this.aiMessages.push({ role: 'user', content: message, toolTrace: [] });
            const assistantMsg = { role: 'assistant', content: '', toolTrace: [], pendingAction: null, error: '' };
            this.aiMessages.push(assistantMsg);
            // Alpine 响应式关键修复：push 后从数组取回 proxy 引用，SSE 事件的全部
            // 变更必须走 liveMsg——写 push 前的裸对象不会触发视图更新（症状：数据层
            // content/toolTrace 都对，但助手气泡永远空白、工具轨迹不渲染）。
            const liveMsg = this.aiMessages[this.aiMessages.length - 1];
            this.aiThinking = true;

            try {
                const resp = await fetch('/api/ai/chat', {
                    method: 'POST',
                    headers: {
                        'Authorization': 'Bearer ' + this.token,
                        'Content-Type': 'application/json',
                        'Accept': 'text/event-stream'
                    },
                    body: JSON.stringify({ conversation_id: this.aiConversationId, message })
                });
                if (!resp.ok || !resp.body) {
                    liveMsg.error = this.t('ai.error_prefix') + ': HTTP ' + resp.status;
                    this.showToast(liveMsg.error, 'error');
                    return;
                }
                const reader = resp.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';
                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    buffer += decoder.decode(value, { stream: true });
                    const lines = buffer.split('\n');
                    buffer = lines.pop();
                    for (const line of lines) {
                        if (!line.startsWith('data: ')) continue;
                        let ev;
                        try { ev = JSON.parse(line.slice(6)); } catch (e) { continue; }
                        this.handleAISSEEvent(ev, liveMsg);
                    }
                }
            } catch (e) {
                liveMsg.error = this.t('ai.error_prefix') + ': ' + e.message;
                this.showToast(liveMsg.error, 'error');
            } finally {
                this.aiThinking = false;
            }
        },

        // 事件协议（web/modules/ai/chat.go sseEvent）：
        //   delta          {type,text}              → 增量拼接进当前 assistant 消息
        //   tool_call      {type,name,args_digest}  → 追加折叠条目（🔧 名称 + digest）
        //   tool_result    {type,name,digest}       → 补全同名条目的 digest（结果摘要）
        //   pending_action {type,...PendingAction}  → 本轮只渲染只读信息卡
        //   done           {type,conversation_id}   → 记录会话 ID、刷新会话列表
        //   error          {type,message}           → toast + 消息内红条
        handleAISSEEvent(ev, assistantMsg) {
            switch (ev.type) {
                case 'delta':
                    assistantMsg.content += ev.text || '';
                    break;
                case 'tool_call':
                    assistantMsg.toolTrace.push({ name: ev.name, digest: ev.args_digest || '' });
                    break;
                case 'tool_result': {
                    const entry = assistantMsg.toolTrace.find(t => t.name === ev.name && !t.resultApplied);
                    if (entry) {
                        entry.digest = ev.digest || entry.digest;
                        entry.resultApplied = true;
                    } else {
                        assistantMsg.toolTrace.push({ name: ev.name, digest: ev.digest || '' });
                    }
                    break;
                }
                case 'pending_action':
                    assistantMsg.pendingAction = {
                        id: ev.id, tool_name: ev.tool_name, args_json: ev.args_json,
                        summary: ev.summary, confirm_word: ev.confirm_word
                    };
                    this.openAIActionConfirm(assistantMsg.pendingAction);
                    break;
                case 'done':
                    this.aiConversationId = ev.conversation_id || this.aiConversationId;
                    this.loadAIConversations();
                    break;
                case 'error':
                    assistantMsg.error = ev.message || this.t('ai.error_prefix');
                    this.showToast(assistantMsg.error, 'error');
                    break;
            }
        },

        // ═══ AI 写操作确认门（type-to-confirm）═══
        // openAIActionConfirm 打开弹窗：confirmWordInput/genPassword 每次重新打开
        // 都清空，不continu上一次残留（尤其密码——绝不允许旧密码跨操作卡复用）。
        openAIActionConfirm(action) {
            if (!action || action.resolved) return;
            this.aiConfirm = { show: true, action, confirmWordInput: '', genPassword: '', submitting: false };
        },
        closeAIActionConfirm() {
            this.aiConfirm = { show: false, action: null, confirmWordInput: '', genPassword: '', submitting: false };
        },
        // aiActionIsCreateUser 供弹窗模板判断是否渲染密码生成器区块。
        aiActionIsCreateUser() {
            return !!(this.aiConfirm.action && this.aiConfirm.action.tool_name === 'create_user');
        },
        // aiGenPassword 用独立的随机源生成一次性密码，不读取/不污染工具箱密码
        // 生成器页面的 this.pwgen 状态（两个生成器互不相关，各自独立成池）。
        // 字符池固定四类全开、20 位，强度足够且不需要用户再做选择。
        aiGenPassword() {
            const upper = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ';
            const lower = 'abcdefghijklmnopqrstuvwxyz';
            const digits = '0123456789';
            const symbols = '!@#$%^&*()-_=+[]{}<>?';
            const groups = [upper, lower, digits, symbols];
            const length = 20;
            const all = groups.join('');
            const randInt = (max) => {
                if (max <= 1) return 0;
                const limit = Math.floor(0x100000000 / max) * max;
                const buf = new Uint32Array(1);
                let x;
                do { crypto.getRandomValues(buf); x = buf[0]; } while (x >= limit);
                return x % max;
            };
            const chars = [];
            for (const g of groups) chars.push(g[randInt(g.length)]);
            while (chars.length < length) chars.push(all[randInt(all.length)]);
            for (let i = chars.length - 1; i > 0; i--) {
                const j = randInt(i + 1);
                [chars[i], chars[j]] = [chars[j], chars[i]];
            }
            this.aiConfirm.genPassword = chars.slice(0, length).join('');
        },
        async aiCopyGenPassword() {
            if (!this.aiConfirm.genPassword) return;
            try {
                await navigator.clipboard.writeText(this.aiConfirm.genPassword);
                this.showToast(this.t('msg.vault_copied'), 'success');
            } catch (e) {
                this.showToast(String(e.message || e), 'error');
            }
        },
        // aiConfirmDisabled 是确认按钮的函数级守卫（不是只靠 :disabled 挡 UI——
        // confirmAIAction 开头也会再次调用它 return，双重防线防止绕过按钮直接
        // 触发事件）：确认词必须逐字匹配服务端下发的 confirm_word；create_user
        // 还要求已经生成过密码（否则确认请求必然 502）。
        aiConfirmDisabled() {
            const action = this.aiConfirm.action;
            if (!action) return true;
            if (this.aiConfirm.submitting) return true;
            if (this.aiConfirm.confirmWordInput !== action.confirm_word) return true;
            if (this.aiActionIsCreateUser() && !this.aiConfirm.genPassword) return true;
            return false;
        },
        async confirmAIAction() {
            if (this.aiConfirmDisabled()) return;
            const action = this.aiConfirm.action;
            this.aiConfirm.submitting = true;
            try {
                const body = { action_id: action.id, confirm_word: this.aiConfirm.confirmWordInput };
                if (this.aiActionIsCreateUser()) {
                    body.client_payload = { password: this.aiConfirm.genPassword };
                }
                const res = await fetch('/api/ai/action/confirm', {
                    method: 'POST',
                    headers: { 'Authorization': 'Bearer ' + this.token, 'Content-Type': 'application/json' },
                    body: JSON.stringify(body)
                });
                const data = await res.json().catch(() => ({}));
                if (!res.ok) {
                    this.showToast(data.error || this.t('ai.error_prefix'), 'error');
                    return;
                }
                this.showToast(this.t('msg.op_success'), 'success');
                // 标记对应消息卡为已执行（绿色态、不可再点，防重复确认 410）
                const doneMsg = [...this.aiMessages].reverse().find(x => x.pendingAction && x.pendingAction.id === action.id);
                if (doneMsg) doneMsg.pendingAction = Object.assign({}, doneMsg.pendingAction, { resolved: true });
                this.closeAIActionConfirm();
            } catch (e) {
                this.showToast(String(e.message || e), 'error');
            } finally {
                this.aiConfirm.submitting = false;
            }
        },
        async cancelAIAction() {
            const action = this.aiConfirm.action;
            if (!action) { this.closeAIActionConfirm(); return; }
            try {
                await fetch('/api/ai/action/cancel', {
                    method: 'POST',
                    headers: { 'Authorization': 'Bearer ' + this.token, 'Content-Type': 'application/json' },
                    body: JSON.stringify({ action_id: action.id })
                });
            } catch (e) {
                // 取消失败也不阻塞关闭弹窗——TTL 到了服务端自然视为不存在。
            }
            this.closeAIActionConfirm();
        },
        // aiActionSummaryKey 把工具名映射到 i18n 摘要文案 key，服务端已经生成
        // 了中文 summary（pendingActionSummary），这里的 key 仅用于弹窗标题下方
        // 的"将执行以下操作"类型标签，不是复述服务端 summary 本身。
        aiActionSummaryKey(toolName) {
            const known = ['set_user_quota', 'create_user', 'add_firewall_rule', 'restart_service', 'run_diagnostics', 'create_folder'];
            return known.includes(toolName) ? ('ai.action_summary_' + toolName) : 'ai.action_summary_default';
        },

        // ═══ 任务引擎（ai 页「任务」tab） ═══
        // GET /api/tasks → {"jobs":[...]}（api() 对非对象/非 JSON 响应已经 toast 过，
        // 这里按计划要求再做一层字符串守卫：必须是带 jobs 数组的对象才接受）。
        async loadAITasks() {
            const data = await this.api('/tasks');
            if (!data || typeof data !== 'object' || !Array.isArray(data.jobs)) {
                if (typeof data === 'string' && data.trim()) this.showToast(data.trim(), 'error');
                return;
            }
            this.taskJobs = data.jobs;
            this.syncTaskJobsPolling();
        },

        // taskRunningOrPaused：running/paused 任一存在即需要轮询刷新进度。
        get taskRunningOrPaused() {
            return this.taskJobs.filter(j => j.status === 'running' || j.status === 'paused');
        },

        // 存在 running/paused 任务时启动 2s 轮询，否则停止——由 loadAITasks()
        // 每次加载后自动校准，不需要调用方单独管理启停（对照 rclone 进度轮询模式，
        // 这里不需要单独任务的细粒度 progress 接口，job 列表本身已带 progress_cur/total）。
        syncTaskJobsPolling() {
            if (this.taskRunningOrPaused.length) {
                this.startTaskJobsPolling();
            } else {
                this.stopTaskJobsPolling();
            }
        },

        startTaskJobsPolling() {
            if (this.taskJobsTimer) return;
            this.taskJobsTimer = setInterval(() => {
                if (this.page !== 'ai') { this.stopTaskJobsPolling(); return; }
                this.loadAITasks();
            }, 2000);
        },

        stopTaskJobsPolling() {
            if (this.taskJobsTimer) { clearInterval(this.taskJobsTimer); this.taskJobsTimer = null; }
        },

        // taskAction：POST /api/tasks/{id}/{action}，action ∈ pause/resume/cancel。
        // REST 层无确认门（该写操作已经过 AI 对话侧的确认卡/或本身是暂停这种无
        // 破坏性操作），cancel 前用原生 confirm() 承担确认语义，与面板其它危险
        // 操作一致（见 msg.*_confirm 系列）。
        async taskAction(id, action) {
            if (action === 'cancel' && !confirm(this.t('msg.task_cancel_confirm'))) return;
            const data = await this.api('/tasks/' + encodeURIComponent(id) + '/' + action, { method: 'POST' });
            if (!data || typeof data !== 'object' || !data.ok) {
                if (typeof data === 'string' && data.trim()) this.showToast(data.trim(), 'error');
                return;
            }
            this.showToast(this.t('msg.task_action_' + action + '_ok'), 'success');
            this.loadAITasks();
        },

        // taskKindLabel/taskStatusLabel：kind/status 映射到 i18n 文案，未知值兜底原值。
        taskKindLabel(kind) {
            const k = 'ai.task_kind_' + kind;
            return window.t(k) !== k ? window.t(k) : kind;
        },

        taskStatusLabel(status) {
            const k = 'ai.task_status_' + status;
            return window.t(k) !== k ? window.t(k) : status;
        },

        // taskStatusBadgeStyle：六态六色，与 i18n/CSS 变量保持一致（黄/蓝/灰/绿/红/灰）。
        taskStatusBadgeStyle(status) {
            const styles = {
                pending_confirm: 'background: var(--warn-light);color: var(--warn)',
                running: 'background: var(--primary-light);color: var(--primary)',
                paused: 'background: var(--card-bg-alt);color: var(--text-secondary)',
                done: 'background: var(--success-light);color: var(--success)',
                failed: 'background: var(--danger-light);color: var(--danger)',
                cancelled: 'background: var(--card-bg-alt);color: var(--text-muted)'
            };
            return styles[status] || 'background: var(--card-bg-alt);color: var(--text-muted)';
        },

        // taskResult：解析 done 任务的 Result JSON（{"moved":N,"freed":M,"skipped":K}），
        // 解析失败（如空字符串/脏数据）兜底 null，调用方据此隐藏报告行。
        taskResult(job) {
            if (!job || !job.result) return null;
            try {
                const r = JSON.parse(job.result);
                if (!r || typeof r !== 'object') return null;
                return r;
            } catch (e) {
                return null;
            }
        },

        // ═══ 通知中心（铃铛 + 建议卡 + 周报，F2 Task 6）═══
        // notificationsVisible：AI 关断时整个通知中心隐藏（triggers/notifications
        // 属 AI 功能面，关断零足迹的 UI 表达，硬约束 1）。模板上用这个 getter
        // 控制铃铛入口的 x-show，不单独在每处重复判断 aiStatus.enabled。
        get notificationsVisible() {
            return !!this.aiStatus.enabled;
        },

        // syncNotifPolling：按 aiStatus.enabled 校准轮询启停，由 init()/toggleAI()/
        // visibilitychange 统一调用（不需要调用方各自判断，对照 taskJobs 的
        // syncTaskJobsPolling 模式）。启用时立即拉一次，不等 30s 第一轮。
        syncNotifPolling() {
            if (this.notificationsVisible) {
                this.loadNotifUnreadCount();
                this.startNotifPolling();
            } else {
                this.notifPanel.open = false;
                this.stopNotifPolling();
            }
        },

        startNotifPolling() {
            if (this.notifPollTimer) return;
            this.notifPollTimer = setInterval(() => {
                if (document.hidden) return; // 双重保险：即便定时器没被 visibilitychange 清掉也不发请求
                this.loadNotifUnreadCount();
            }, 30000);
        },

        stopNotifPolling() {
            if (this.notifPollTimer) { clearInterval(this.notifPollTimer); this.notifPollTimer = null; }
        },

        // GET /api/notifications/unread-count → {"count":N}，30s 轮询驱动铃铛徽标。
        async loadNotifUnreadCount() {
            const data = await this.api('/notifications/unread-count');
            if (data && typeof data === 'object' && typeof data.count === 'number') {
                this.notifPanel.unreadCount = data.count;
            }
        },

        // toggleNotifPanel：点击铃铛展开/收起。展开时加载三段数据（建议卡/
        // 周报/通知列表），收起不清空数据（下次展开前轮询仍可能刷新未读数）。
        toggleNotifPanel() {
            this.notifPanel.open = !this.notifPanel.open;
            if (this.notifPanel.open) this.loadNotifPanelData();
        },

        closeNotifPanel() {
            this.notifPanel.open = false;
        },

        // loadNotifPanelData：并行拉通知列表 + 建议卡；周报从通知列表里挑最新
        // 一条 kind=report 的渲染（不是单独端点，计划 Task 6 交付明细 2 原文）。
        async loadNotifPanelData() {
            this.notifPanel.loading = true;
            try {
                const [notifData, sugData] = await Promise.all([
                    this.api('/notifications?limit=20'),
                    this.api('/triggers/suggestions')
                ]);
                if (notifData && typeof notifData === 'object' && Array.isArray(notifData.notifications)) {
                    this.notifPanel.notifications = notifData.notifications;
                    this.notifPanel.report = notifData.notifications.find(n => n.kind === 'report') || null;
                }
                if (sugData && typeof sugData === 'object' && Array.isArray(sugData.suggestions)) {
                    this.notifPanel.suggestions = sugData.suggestions;
                }
            } finally {
                this.notifPanel.loading = false;
            }
        },

        // notifParseData：data_json/params_json JSON.parse 失败容错——坏 JSON
        // 不炸渲染，调用方拿不到对象时退回空对象，t() 插值自然跳过所有 {key}
        // 占位符替换（硬约束 6）。
        notifParseData(jsonStr) {
            if (!jsonStr) return {};
            try {
                const v = JSON.parse(jsonStr);
                return (v && typeof v === 'object') ? v : {};
            } catch (e) {
                return {};
            }
        },

        // notifTitle/notifBody：对通知/建议的 title/body i18n 键做 t() 插值渲染；
        // 键缺失时 window.t 已经兜底返回键名最后一段，这里不用再额外处理（硬约束
        // 3 的"键缺失不炸页面"由 i18n.js 的 translateWith 统一兜底）。
        notifTitle(item) {
            return this.t(item.title, this.notifParseData(item.params_json || item.data_json));
        },
        notifBody(item) {
            return this.t(item.body, this.notifParseData(item.params_json || item.data_json));
        },

        // notifSuggestionActions：解析建议卡 actions_json 第一项，用于判断按钮
        // 展示（task_plan→执行；navigate→去看看）。坏 JSON/空数组兜底空对象。
        notifSuggestionAction(sg) {
            try {
                const actions = JSON.parse(sg.actions_json || '[]');
                return (Array.isArray(actions) && actions.length) ? actions[0] : {};
            } catch (e) {
                return {};
            }
        },

        // notifDismissSuggestion：POST /api/triggers/suggestions/{id}/dismiss，
        // 成功后从列表移除（不整面板重拉，减少一次网络往返）。
        async notifDismissSuggestion(id) {
            const data = await this.api('/triggers/suggestions/' + encodeURIComponent(id) + '/dismiss', { method: 'POST' });
            if (!data || typeof data !== 'object' || !data.ok) {
                if (typeof data === 'string' && data.trim()) this.showToast(data.trim(), 'error');
                return;
            }
            this.notifPanel.suggestions = this.notifPanel.suggestions.filter(s => s.id !== id);
        },

        // notifSuggestionNavigate：navigate 类建议卡「去看看」= navigate(page) +
        // dismiss 一次完成（硬约束 9，navigate 类建议不走 accept 端点）。
        notifSuggestionNavigate(sg) {
            const action = this.notifSuggestionAction(sg);
            if (action.page) this.navigate(action.page);
            this.closeNotifPanel();
            this.notifDismissSuggestion(sg.id);
        },

        // notifAcceptSuggestion：task_plan 类建议卡「执行」→ POST accept →
        // {action_id, confirm_word, summary, job_id} → 开 notifConfirm 弹窗
        // （独立状态对象，不复用 aiConfirm 实例，硬约束 8）。
        async notifAcceptSuggestion(sg) {
            const data = await this.api('/triggers/suggestions/' + encodeURIComponent(sg.id) + '/accept', { method: 'POST' });
            if (!data || typeof data !== 'object' || !data.action_id) {
                if (typeof data === 'string' && data.trim()) this.showToast(data.trim(), 'error');
                return;
            }
            this.notifPanel.suggestions = this.notifPanel.suggestions.filter(s => s.id !== sg.id);
            this.notifConfirm = { show: true, action: data, confirmWordInput: '', submitting: false };
        },

        closeNotifConfirm() {
            this.notifConfirm = { show: false, action: null, confirmWordInput: '', submitting: false };
        },

        notifConfirmDisabled() {
            const action = this.notifConfirm.action;
            if (!action) return true;
            if (this.notifConfirm.submitting) return true;
            return this.notifConfirm.confirmWordInput !== action.confirm_word;
        },

        // confirmNotifAction：复用既有 POST /api/ai/action/confirm（零新确认
        // 语义，与 chat 确认门同一条路径）。成功后 toast + 跳任务 tab +
        // loadAITasks()（硬约束 8 逐字）。
        async confirmNotifAction() {
            if (this.notifConfirmDisabled()) return;
            const action = this.notifConfirm.action;
            this.notifConfirm.submitting = true;
            try {
                const res = await fetch('/api/ai/action/confirm', {
                    method: 'POST',
                    headers: { 'Authorization': 'Bearer ' + this.token, 'Content-Type': 'application/json' },
                    body: JSON.stringify({ action_id: action.action_id, confirm_word: this.notifConfirm.confirmWordInput })
                });
                const data = await res.json().catch(() => ({}));
                if (!res.ok) {
                    this.showToast(data.error || this.t('ai.error_prefix'), 'error');
                    return;
                }
                this.showToast(this.t('msg.op_success'), 'success');
                this.closeNotifConfirm();
                this.closeNotifPanel();
                this.navigate('ai');
                this.aiTab = 'tasks';
                this.loadAITasks();
            } catch (e) {
                this.showToast(String(e.message || e), 'error');
            } finally {
                this.notifConfirm.submitting = false;
            }
        },

        // notifReportStorageText/notifReportTasksText：周报卡三块数字渲染，大小
        // 走既有 formatSize 复用（硬约束 10，不重写格式化逻辑）。report.data_json
        // JSON.parse 失败容错，拿不到字段时用 0/'-' 兜底，不炸渲染。
        notifReportData(report) {
            return this.notifParseData(report && report.data_json);
        },

        // notifClickNotification：点击通知列表项→标记已读 + 按 kind 跳转
        // （task→任务 tab；health→健康页；其余 kind 不跳转，只标已读）。
        async notifClickNotification(n) {
            if (!n.read) await this.notifMarkRead(n.id);
            if (n.kind === 'task') {
                this.closeNotifPanel();
                this.navigate('ai');
                this.aiTab = 'tasks';
                this.loadAITasks();
            } else if (n.kind === 'suggestion') {
                // suggestion 通知本身只是提示，具体动作在建议卡区——跳转到面板
                // 顶部即可，不额外导航到业务页。
            } else if ((n.title || '').indexOf('notif.health_') === 0) {
                this.closeNotifPanel();
                this.navigate('health');
            }
        },

        async notifMarkRead(id) {
            const data = await this.api('/notifications/' + encodeURIComponent(id) + '/read', { method: 'POST' });
            if (data && typeof data === 'object' && data.ok) {
                const item = this.notifPanel.notifications.find(x => x.id === id);
                if (item) item.read = true;
                this.loadNotifUnreadCount();
            }
        },

        async notifMarkAllRead() {
            const data = await this.api('/notifications/read-all', { method: 'POST' });
            if (data && typeof data === 'object' && data.ok) {
                this.notifPanel.notifications.forEach(n => { n.read = true; });
                this.loadNotifUnreadCount();
            }
        },

        async notifClearRead() {
            const data = await this.api('/notifications/clear', { method: 'POST' });
            if (!data || typeof data !== 'object' || !data.ok) {
                if (typeof data === 'string' && data.trim()) this.showToast(data.trim(), 'error');
                return;
            }
            this.notifPanel.notifications = this.notifPanel.notifications.filter(n => !n.read);
        },

        // notifGenerateReport：周报卡「重新生成」→ POST generate 后刷新列表
        // （硬约束 10 逐字）。
        async notifGenerateReport() {
            const data = await this.api('/triggers/report/generate', { method: 'POST' });
            if (!data || typeof data !== 'object' || !data.notification_id) {
                if (typeof data === 'string' && data.trim()) this.showToast(data.trim(), 'error');
                return;
            }
            this.showToast(this.t('notif.report_regen_ok'), 'success');
            this.loadNotifPanelData();
        },

        // 面板更新（OTA 签名校验）
        async checkPanelUpdate() {
            const data = await this.api('/update/check');
            if (data) {
                this.panelUpdate = { current: data.current || '', latest: data.latest || '', has_update: !!data.has_update, error: data.error || '', detail: '' };
            }
        },
        async upgradePanel() {
            if (!confirm(this.t('msg.panel_upgrade_confirm'))) return;
            this.panelUpdating = true;
            const data = await this.api('/update/upgrade', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: 'confirm=yes' });
            if (data) {
                this.showToast(this.t('msg.panel_upgrade_started'), 'success');
                this.panelUpdate.detail = this.t('msg.panel_upgrade_running');
            }
            this.panelUpdating = false;
        },

        async restoreBackup(file) {
            if (!confirm(this.t('msg.restore_backup_confirm'))) return;
            this.showToast(this.t('msg.restoring'), 'success');
            const data = await this.api('/backup/restore', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `file=${encodeURIComponent(file)}`
            });
            if (data) {
                this.showToast(this.t('msg.restore_done_restarted'), 'success');
            }
        },

        async deleteBackup(file) {
            if (!confirm(this.t('msg.del_backup_confirm'))) return;
            const data = await this.api('/backup/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `file=${encodeURIComponent(file)}`
            });
            if (data) {
                this.showToast(this.t('msg.backup_deleted'), 'success');
                this.loadBackups();
            }
        },

        // ═══ Rclone 远端同步 ═══
        async loadRcloneStatus() {
            const data = await this.api('/rclone/status');
            if (data) this.rcloneStatus = data;
        },

        async loadRcloneRemotes() {
            const data = await this.api('/rclone/remotes');
            if (data) this.rcloneRemotes = data.remotes || [];
        },

        get rcloneRunningTasks() {
            return this.rcloneTasks.filter(t => t.last_result === 'running');
        },

        async loadRcloneTasks() {
            const data = await this.api('/rclone/tasks');
            if (data) this.rcloneTasks = data.tasks || [];
        },

        // 运行中任务的实时进度轮询（3s 一轮）：刷新任务列表 + 拉每个运行中任务的 progress
        startRcloneProgressPolling() {
            if (this.rcloneProgressTimer) return;
            this.rcloneWasRunning = this.rcloneRunningTasks.map(t => t.id);
            const tick = async () => {
                if (this.page !== 'rclone') { this.stopRcloneProgressPolling(); return; }
                await this.loadRcloneTasks();
                const running = this.rcloneRunningTasks;
                // 检测刚结束的任务 → 提示结果
                const nowRunning = running.map(t => t.id);
                for (const id of this.rcloneWasRunning) {
                    if (!nowRunning.includes(id)) {
                        const t = this.rcloneTasks.find(x => x.id === id);
                        if (t && t.last_result === 'success') {
                            this.showToast(this.t('msg.task_finished', [t.name]), 'success');
                        } else if (t && t.last_result === 'failed') {
                            this.showToast(this.t('msg.task_failed', [t.name]) + (t.last_message ? ': ' + t.last_message : ''), 'error');
                        }
                    }
                }
                this.rcloneWasRunning = nowRunning;
                for (const t of running) {
                    const data = await this.api('/rclone/tasks/' + encodeURIComponent(t.id) + '/progress');
                    if (data) this.rcloneProgressMap[t.id] = data;
                }
                if (!running.length) {
                    this.stopRcloneProgressPolling();
                    this.loadRcloneLogs();
                }
            };
            tick();
            this.rcloneProgressTimer = setInterval(tick, 3000);
        },

        stopRcloneProgressPolling() {
            if (this.rcloneProgressTimer) { clearInterval(this.rcloneProgressTimer); this.rcloneProgressTimer = null; }
        },

        async loadSharedDirs() {
            const data = await this.api('/rclone/shared-dirs');
            if (data) this.sharedDirs = data.dirs || [];
        },

        async loadRcloneLogs() {
            const data = await this.api('/rclone/logs');
            if (data) this.rcloneLogs = data.logs || [];
        },

        blankRemoteForm() {
            return { name: '', type: '', provider: 'AWS', endpoint: '', access_key_id: '', secret_access_key: '', region: '', host: '', port: '22', user: '', pass: '', url: '', vendor: 'nextcloud', local_path: '' };
        },

        blankTaskForm() {
            return { name: '', direction: 'upload', source: '', sub_path: '', remote: '', dest_path: '', mode: 'sync', schedule: '', bandwidth: 0, transfers: 4 };
        },

        openAddRemote() {
            this.editingRemote = '';
            this.remoteForm = this.blankRemoteForm();
            this.showAddRemote = true;
        },

        async editRemote(name) {
            const data = await this.api('/rclone/remotes/' + encodeURIComponent(name));
            if (!data || data.error) {
                this.showToast(this.t('msg.read_remote_failed') + ': ' + (data && data.error || ''), 'error');
                return;
            }
            const f = this.blankRemoteForm();
            f.name = data.name;
            f.type = data.type;
            const c = data.config || {};
            // 回填各类型字段（敏感字段后端返回 ********，提交时后端会跳过）
            f.provider = c.provider || f.provider;
            f.endpoint = c.endpoint || '';
            f.access_key_id = c.access_key_id || '';
            f.secret_access_key = c.secret_access_key || '';
            f.region = c.region || '';
            f.host = c.host || '';
            f.port = c.port || (data.type === 'ftp' ? '21' : '22');
            f.user = c.user || '';
            f.pass = c.pass || '';
            f.url = c.url || '';
            f.vendor = c.vendor || f.vendor;
            f.local_path = c.local_path || c.path || '';
            this.remoteForm = f;
            this.editingRemote = name;
            this.showAddRemote = true;
        },

        async createRemote() {
            if (!this.remoteForm.name || !this.remoteForm.type) {
                this.showToast(this.t('msg.fill_name_type'), 'error');
                return;
            }
            this.remoteCreating = true;
            let body = `name=${encodeURIComponent(this.remoteForm.name)}&type=${encodeURIComponent(this.remoteForm.type)}`;
            body += this.remoteConfigBody();
            const data = await this.api('/rclone/remotes', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            this.remoteCreating = false;
            if (data && !data.error) {
                this.showToast(this.t('msg.remote_created'), 'success');
                this.showAddRemote = false;
                this.remoteForm = this.blankRemoteForm();
                this.loadRcloneRemotes();
            } else if (data && data.error) {
                this.showToast(this.t('msg.create_failed') + ': ' + data.error, 'error');
            }
        },

        // remoteConfigBody 根据类型生成 rc_* 参数（创建/更新共用）
        remoteConfigBody() {
            const f = this.remoteForm;
            let body = '';
            if (f.type === 's3') {
                body += `&rc_provider=${encodeURIComponent(f.provider)}&rc_endpoint=${encodeURIComponent(f.endpoint)}`;
                body += `&rc_access_key_id=${encodeURIComponent(f.access_key_id)}&rc_secret_access_key=${encodeURIComponent(f.secret_access_key)}`;
                if (f.region) body += `&rc_region=${encodeURIComponent(f.region)}`;
            } else if (f.type === 'sftp') {
                body += `&rc_host=${encodeURIComponent(f.host)}&rc_port=${encodeURIComponent(f.port)}`;
                body += `&rc_user=${encodeURIComponent(f.user)}&rc_pass=${encodeURIComponent(f.pass)}`;
            } else if (f.type === 'webdav') {
                body += `&rc_url=${encodeURIComponent(f.url)}&rc_vendor=${encodeURIComponent(f.vendor)}`;
                body += `&rc_user=${encodeURIComponent(f.user)}&rc_pass=${encodeURIComponent(f.pass)}`;
            } else if (f.type === 'ftp') {
                body += `&rc_host=${encodeURIComponent(f.host)}&rc_port=${encodeURIComponent(f.port)}`;
                body += `&rc_user=${encodeURIComponent(f.user)}&rc_pass=${encodeURIComponent(f.pass)}`;
            } else if (f.type === 'local') {
                body += `&rc_local_path=${encodeURIComponent(f.local_path)}`;
            }
            return body;
        },

        async saveRemote() {
            if (!this.editingRemote) {
                return this.createRemote();
            }
            this.remoteCreating = true;
            const body = this.remoteConfigBody().replace(/^&/, '');
            const data = await this.api('/rclone/remotes/' + encodeURIComponent(this.editingRemote), {
                method: 'PUT',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            this.remoteCreating = false;
            if (data && !data.error) {
                this.showToast(this.t('msg.remote_updated'), 'success');
                this.showAddRemote = false;
                this.editingRemote = '';
                this.loadRcloneRemotes();
            } else if (data && data.error) {
                this.showToast(this.t('msg.save_failed') + ': ' + data.error, 'error');
            }
        },

        async deleteRemote(name) {
            if (!confirm(this.t('msg.del_remote_confirm', [name]))) return;
            const data = await this.api('/rclone/remotes/' + encodeURIComponent(name), { method: 'DELETE' });
            if (data && !data.error) {
                this.showToast(this.t('msg.remote_deleted'), 'success');
                this.loadRcloneRemotes();
            } else if (data && data.error) {
                this.showToast(this.t('msg.del_failed') + ': ' + data.error, 'error');
            }
        },

        async testRemote(name) {
            this.remoteTesting = name;
            const data = await this.api('/rclone/remotes/test', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `name=${encodeURIComponent(name)}`
            });
            this.remoteTesting = '';
            if (data) {
                if (data.ok) {
                    this.showToast(this.t('msg.conn_success') + ': ' + name, 'success');
                } else {
                    this.showToast(this.t('msg.conn_failed') + ': ' + (data.error || data.message), 'error');
                }
            }
        },

        // 任务本地完整路径 = 共享目录 + 可选子路径
        taskFullSource() {
            const t = this.taskForm;
            if (!t.sub_path) return t.source;
            const sub = t.sub_path.replace(/^\/+|\/+$/g, '');
            return sub ? t.source + '/' + sub : t.source;
        },

        openAddTask() {
            this.editingTask = null;
            this.taskForm = this.blankTaskForm();
            this.showTaskAdvanced = false;
            this.showAddTask = true;
        },

        editTask(task) {
            // 拆分 source 为 共享目录 + 子路径
            const f = this.blankTaskForm();
            f.name = task.name;
            f.direction = task.direction || 'upload';
            f.remote = task.remote;
            f.dest_path = task.dest_path;
            f.mode = task.mode;
            f.schedule = task.schedule || '';
            f.bandwidth = task.bandwidth || 0;
            f.transfers = task.transfers || 4;
            f.source = task.source;
            // 若 source 是某个共享目录的子路径，拆出 sub_path
            for (const d of this.sharedDirs) {
                if (task.source.startsWith(d + '/')) {
                    f.source = d;
                    f.sub_path = task.source.slice(d.length + 1);
                    break;
                }
            }
            this.taskForm = f;
            this.editingTask = task;
            this.showTaskAdvanced = !!(task.schedule || task.bandwidth > 0);
            this.showAddTask = true;
        },

        async createSubDir() {
            const t = this.taskForm;
            if (!t.source || !t.sub_path) return;
            const data = await this.api('/rclone/mkdir', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `dir=${encodeURIComponent(t.source)}&sub=${encodeURIComponent(t.sub_path)}`
            });
            if (data && !data.error) {
                this.showToast(this.t('msg.dir_created') + ': ' + data.path, 'success');
            } else if (data && data.error) {
                this.showToast(this.t('msg.create_failed') + ': ' + data.error, 'error');
            }
        },

        async saveTask() {
            if (this.editingTask) {
                return this.updateTask();
            }
            return this.createTask();
        },

        async createTask() {
            const fullSource = this.taskFullSource();
            if (!this.taskForm.name || !fullSource || !this.taskForm.remote || !this.taskForm.dest_path) {
                this.showToast(this.t('msg.fill_required'), 'error');
                return;
            }
            this.taskCreating = true;
            const t = this.taskForm;
            const body = `name=${encodeURIComponent(t.name)}&direction=${encodeURIComponent(t.direction)}&source=${encodeURIComponent(fullSource)}&remote=${encodeURIComponent(t.remote)}&dest_path=${encodeURIComponent(t.dest_path)}&mode=${encodeURIComponent(t.mode)}&schedule=${encodeURIComponent(t.schedule)}&bandwidth=${t.bandwidth}&transfers=${t.transfers}`;
            const data = await this.api('/rclone/tasks', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            this.taskCreating = false;
            if (data && !data.error) {
                this.showToast(this.t('msg.task_created'), 'success');
                this.showAddTask = false;
                this.taskForm = this.blankTaskForm();
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast(this.t('msg.create_failed') + ': ' + data.error, 'error');
            }
        },

        async updateTask() {
            const fullSource = this.taskFullSource();
            const t = this.taskForm;
            if (!t.name || !fullSource || !t.remote || !t.dest_path) {
                this.showToast(this.t('msg.fill_required'), 'error');
                return;
            }
            this.taskCreating = true;
            const body = `name=${encodeURIComponent(t.name)}&direction=${encodeURIComponent(t.direction)}&source=${encodeURIComponent(fullSource)}&remote=${encodeURIComponent(t.remote)}&dest_path=${encodeURIComponent(t.dest_path)}&mode=${encodeURIComponent(t.mode)}&schedule=${encodeURIComponent(t.schedule)}&bandwidth=${t.bandwidth}&transfers=${t.transfers}`;
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(this.editingTask.id), {
                method: 'PUT',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            this.taskCreating = false;
            if (data && !data.error) {
                this.showToast(this.t('msg.task_updated'), 'success');
                this.showAddTask = false;
                this.editingTask = null;
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast(this.t('msg.save_failed') + ': ' + data.error, 'error');
            }
        },

        async deleteTask(id) {
            if (!confirm(this.t('msg.del_task_confirm'))) return;
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(id), { method: 'DELETE' });
            if (data && !data.error) {
                this.showToast(this.t('msg.task_deleted'), 'success');
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast(this.t('msg.del_failed') + ': ' + data.error, 'error');
            }
        },

        async runTask(id) {
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(id) + '/run', { method: 'POST' });
            if (data && !data.error) {
                this.showToast(this.t('msg.task_started'), 'success');
                await this.loadRcloneTasks();
                this.startRcloneProgressPolling();
            } else if (data && data.error) {
                this.showToast(this.t('msg.start_failed') + ': ' + data.error, 'error');
            }
        },

        async toggleTask(id) {
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(id) + '/toggle', { method: 'POST' });
            if (data && !data.error) {
                this.showToast(this.t('msg.status_toggled'), 'success');
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast(this.t('msg.op_failed') + ': ' + data.error, 'error');
            }
        },

        async clearRcloneLogs() {
            if (!confirm(this.t('msg.clear_sync_logs_confirm'))) return;
            const data = await this.api('/rclone/logs', { method: 'DELETE' });
            if (data && !data.error) {
                this.showToast(this.t('msg.logs_cleared'), 'success');
                this.loadRcloneLogs();
            }
        },

        // ═══ Diagnostics ═══
        async loadDiagnostics() {
            const data = await this.api('/diagnostics/status');
            if (data) this.diagItems = data.items || [];
            const hdata = await this.api('/diagnostics/history');
            if (hdata) this.diagHistory = hdata.history || [];
            const cdata = await this.api('/diagnostics/config');
            if (cdata) {
                if (cdata.config) this.diagConfig = cdata.config;
                if (cdata.items) {
                    for (const it of cdata.items) {
                        const existing = this.diagItems.find(i => i.id === it.id);
                        if (existing) {
                            existing.enabled = it.enabled;
                            existing.schedule = it.schedule;
                        }
                    }
                }
            }
        },

        async runDiagnostic(itemId) {
            if (!confirm(this.t('msg.diag_confirm'))) return;
            this.showToast(this.t('msg.diag_started'), 'success');
            const data = await this.api('/diagnostics/run', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `item_id=${encodeURIComponent(itemId)}`
            });
            if (data) {
                this.showToast(this.t('msg.started'), 'success');
                setTimeout(() => this.loadDiagnostics(), 3000);
            }
        },

        async toggleDiagItem(itemId, enabled) {
            await this.api('/diagnostics/config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `item_id=${encodeURIComponent(itemId)}&enabled=${enabled}`
            });
            this.loadDiagnostics();
        },

        async setDiagSchedule(itemId, schedule) {
            await this.api('/diagnostics/config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `item_id=${encodeURIComponent(itemId)}&schedule=${encodeURIComponent(schedule)}`
            });
            this.showToast(this.t('msg.saved'), 'success');
        },

        async saveDiagConfig() {
            const c = this.diagConfig;
            const body = `time_window_start=${encodeURIComponent(c.time_window_start)}&time_window_end=${encodeURIComponent(c.time_window_end)}&temp_limit=${c.temp_limit}&io_limit=${c.io_limit}&scrub_speed_max=${c.scrub_speed_max}`;
            await this.api('/diagnostics/config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: body
            });
            this.showToast(this.t('msg.diag_saved'), 'success');
        }
    };
}
