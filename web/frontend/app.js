function nasPanel() {
    return {
        token: localStorage.getItem('nas_token') || '',
        page: 'dashboard',
        navGroup: 'overview',
        loading: false,
        loginError: '',
        loginForm: { username: '', password: '' },
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
            { label: this.t('msg.ftp_passive'), port: '30000:31000', proto: 'tcp' },
            { label: 'NFS (2049)', port: '2049', proto: 'any' },
            { label: 'WebDAV (8080)', port: '8080', proto: 'tcp' },
            { label: this.t('msg.panel_port'), port: '8090', proto: 'tcp' },
        ],
        firewallForm: { port: '', proto: 'tcp', action: 'allow', from: '', comment: '' },
        firewallSaving: false,
        monitor: {},
        monitorTimer: null,
        monitorShowDetail: false,
        monitorShowAdvanced: false,
        alertConfig: {},
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
        // Rclone
        rcloneStatus: {},
        rcloneRemotes: [],
        rcloneTasks: [],
        sharedDirs: [],
        rcloneLogs: [],
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
        componentCategories: ['文件共享', '网页文件管理', '对象存储', '网页管理', '系统防护', '存储管理', '运行环境'],

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
        // Users module - new
        userTab: 'list', // list | groups | matrix | logs
        userGroups: [],
        groupForm: { name: '', comment: '', members: '' },
        showGroupModal: false,
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

        init() {
            if (this.token) {
                this.navigate('dashboard');
            }
            // Listen for i18n changes
            window.addEventListener('i18n:changed', (e) => {
                this.lang = e.detail.lang;
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
                'filebrowser': 'services.desc_filebrowser',
                'rclone-s3': 'services.desc_s3',
                'fail2ban': 'services.desc_fail2ban',
            };
            return map[name] ? window.t(map[name]) : fallback;
        },

        // 组件分类名翻译（后端返回中文分类，仅用于显示）
        catLabel(cat) {
            const map = {
                '文件共享': 'component.file_sharing',
                '网页文件管理': 'component.web_file_mgmt',
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
                'filebrowser': 'services.filebrowser',
            };
            return map[name] ? window.t(map[name]) : fallback;
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
                const res = await fetch('/api/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: `username=${encodeURIComponent(this.loginForm.username)}&password=${encodeURIComponent(this.loginForm.password)}`
                });
                const data = await res.json();
                if (res.ok) {
                    this.token = data.token;
                    localStorage.setItem('nas_token', this.token);
                    this.navigate('dashboard');
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
        },

        navigate(page) {
            this.page = page;
            switch (page) {
                case 'dashboard': this.loadDashboard(); break;
                case 'services': this.loadServices(); break;
                case 'users': this.loadUsers(); this.loadUserGroups(); this.loadPermMatrix(); this.loadLoginLogs(); break;
                case 'diskmgmt': this.loadStorageOverview(); this.loadWizardStatus(); this.loadSharedFolders(); this.loadPendingOps(); this.loadUsers(); break;
                case 'firewall': this.loadFirewall(); break;
                case 'monitor': this.initMonitorRefresh(); this.loadAlertConfig(); break;
                case 'system': this.loadSystemOverview(); break;
                case 'about': this.loadComponents(); break;
                case 'notice': this.loadNotice(); break;
                case 'backup': this.loadBackups(); break;
                case 'rclone': this.loadRcloneStatus(); this.loadRcloneRemotes(); this.loadRcloneTasks(); this.loadRcloneLogs(); this.loadSharedDirs(); break;
                case 'logs': this.loadAuditLogs(); break;
                case 'diagnostics': this.loadDiagnostics(); break;
            }
        },

        async loadDashboard() {
            this.dashboardLoaded = false;
            const data = await this.api('/dashboard');
            if (data) this.dashboard = data;
            // Also load storage overview for disk bay diagram
            const sdata = await this.api('/disk/overview');
            if (sdata && sdata.overview) this.storageOverview = sdata.overview;
            // Check config consistency
            const cdata = await this.api('/disk/config/check');
            if (cdata) this.configIssues = cdata;
            this.dashboardLoaded = true;
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
                    this.installMsg = data.message || this.t('msg.install_done');
                    this.showToast(data.message || this.t('msg.install_done'), 'success');
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
                    const steps = (data.steps || []).join(' → ');
                    this.showToast(data.message || this.t('msg.x_install_done', [name]), 'success');
                    this.installMsg = `${name}: ${steps || data.message}`;
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
                this.showToast(data.message || this.t('msg.op_success'), 'success');
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
                this.showToast(data.message || this.t('msg.add_success'), 'success');
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
                this.showToast(data.message || this.t('msg.modify_success'), 'success');
                this.pwdModal = false;
            }
        },

        async deleteUser(username) {
            if (!confirm(this.t('msg.del_user_confirm', [username]))) return;
            const data = await this.api(`/users/${username}?delete_data=false`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || this.t('msg.del_success'), 'success');
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
                this.showToast(data.message || this.t('msg.create_success'), 'success');
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
                this.showToast(data.message || this.t('msg.perm_updated'), 'success');
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
                this.showToast(data.message || this.t('msg.quota_updated'), 'success');
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
                this.showToast(data.message || this.t('msg.create_success'), 'success');
                this.showGroupModal = false;
                this.loadUserGroups();
            }
        },

        async deleteGroup(name) {
            if (!confirm(this.t('msg.del_group_confirm', [name]))) return;
            const data = await this.api(`/user-groups/${name}`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || this.t('msg.del_success'), 'success');
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
                this.showToast(data.message || this.t('msg.members_updated'), 'success');
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
                this.showToast(data.message || this.t('msg.perm_matrix_updated'), 'success');
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
                this.showToast(data.message || this.t('msg.rule_added'), 'success');
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
                this.showToast(data.message || this.t('msg.rule_deleted'), 'success');
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
                this.showToast(data.message || this.t('msg.op_success'), 'success');
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
                this.showToast(data.message || this.t('msg.save_success'), 'success');
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
                this.showToast(data.message || this.t('msg.add_success'), 'success');
                this.showAddShare = false;
                this.shareForm = { name: '', path: '', comment: '', valid_users: '', read_only: false };
                this.loadSambaShares();
            }
        },

        async deleteSambaShare(name) {
            if (!confirm(this.t('msg.del_share_confirm', [name]))) return;
            const data = await this.api(`/config/samba/share?name=${name}`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || this.t('msg.del_success'), 'success');
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
                this.showToast(data.message || this.t('msg.add_success'), 'success');
                this.ftpUserForm.username = '';
                this.loadVsftpdUsers();
            }
        },

        async removeFtpUser(username) {
            if (!confirm(this.t('msg.ftp_remove_confirm', [username]))) return;
            const data = await this.api(`/config/vsftpd-users?username=${username}`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || this.t('msg.remove_success'), 'success');
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
            if (data) this.showToast(data.message || this.t('msg.save_success'), 'success');
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
                this.showToast(data.message || this.t('msg.op_success'), 'success');
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
                this.showToast(data.message || this.t('msg.create_success'), 'success');
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
                this.showToast(data.message || this.t('msg.mount_success'), 'success');
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
                this.showToast(data.message || this.t('msg.unmount_success'), 'success');
                this.unmountForm.target = '';
                this.loadDiskMounts();
            }
        },

        async diskFormat() {
            if (!this.formatForm.device) { this.showToast(this.t('msg.enter_device'), 'error'); return; }
            if (!confirm(this.t('msg.format_confirm', [this.formatForm.device, this.formatForm.fstype]))) return;
            const data = await this.api('/disk/format', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `device=${encodeURIComponent(this.formatForm.device)}&fstype=${this.formatForm.fstype}&confirm=yes`
            });
            if (data) {
                this.showToast(data.message || this.t('msg.format_success'), 'success');
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
            const opts = this.wizard.raid_options.filter(o => {
                if (o.goal !== this.wizGoal) return false;
                if (this.wizDisks && this.wizDisks.length > 0) {
                    if (this.wizDisks.length < o.min_disks) return false;
                    if (o.max_disks > 0 && this.wizDisks.length > o.max_disks) return false;
                }
                return true;
            });
            if (opts.length === 0) {
                return this.wizard.raid_options.filter(o => {
                    if (this.wizDisks && this.wizDisks.length > 0) {
                        if (this.wizDisks.length < o.min_disks) return false;
                        if (o.max_disks > 0 && this.wizDisks.length > o.max_disks) return false;
                    }
                    return true;
                });
            }
            return opts;
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
                const resp = await fetch(`/api/disk/wizard/setup-stream?mode=${this.wizMode}&confirm=yes`, {
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
                this.showToast(data.message || this.t('msg.folder_created'), 'success');
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
                this.showToast(data.message || this.t('msg.folder_deleted'), 'success');
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
                this.showToast(data.message || this.t('msg.perm_matrix_updated'), 'success');
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

            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = this.t('msg.reset_progress');

            try {
                const resp = await fetch(`/api/disk/wizard/reset-stream?confirm=yes`, {
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
                md_device: pool ? pool.device : '',
                old_device: '',
                new_device: '',
                pool_name: pool ? pool.display_name : ''
            };
            this.showReplaceDisk = true;
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
                this.showToast(data.message || this.t('msg.replace_done'), 'success');
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
                this.showToast(data.message || this.t('msg.config_sync_done'), 'success');
                const cdata = await this.api('/disk/config/check');
                if (cdata) this.configIssues = cdata;
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
                this.showToast(data.message || this.t('msg.cleared'), 'success');
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
                this.showToast(data.message || this.t('msg.logs_cleared'), 'success');
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
                this.showToast(data.message || this.t('msg.logs_cleared'), 'success');
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
                this.showToast(data.message || this.t('msg.scrub_started'), 'success');
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
                this.showToast(data.message || this.t('msg.smart_started'), 'success');
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
                pool_name: pool.name,
                pool_type: pool.type,
                pool_device: pool.device || '',
                confirm: 'yes'
            });
            const data = await this.api('/disk/pool/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(data.message || this.t('msg.pool_deleted'), 'success');
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
            if (data) this.showToast(data.message || this.t('msg.modify_success'), 'success');
        },

        async saveTimezone() {
            const data = await this.api('/system/timezone', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `timezone=${encodeURIComponent(this.sysSettings.timezone)}`
            });
            if (data) this.showToast(data.message || this.t('msg.timezone_updated'), 'success');
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
            if (data) this.showToast(data.message || this.t('msg.ssh_updated'), 'success');
        },

        async saveSysctl(param) {
            const data = await this.api('/system/sysctl', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `key=${encodeURIComponent(param.key)}&value=${encodeURIComponent(param.value)}`
            });
            if (data) this.showToast(data.message || this.t('msg.saved'), 'success');
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
                this.showToast(data.message || this.t('msg.done'), 'success');
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
                this.showToast(data.message || this.t('msg.x_success', [actionText[action]]), 'success');
                this.loadSystemOverview();
            }
        },

        async resetSystem() {
            if (!confirm(this.t('msg.factory_reset_confirm'))) return;
            if (!confirm(this.t('msg.factory_reset_final'))) return;
            this.resetMsg = this.t('msg.resetting_wait');
            const data = await this.api('/system/reset', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: 'confirm=yes'
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
                    this.showToast(data.message || this.t('msg.restore_done'), 'success');
                    this.resetMsg = data.message || this.t('msg.restore_done');
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
                this.showToast(data.message || this.t('msg.cert_generated'), 'success');
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
                this.showToast(data.message || this.t('msg.cert_uploaded'), 'success');
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
                this.showToast(data.message || this.t('msg.cert_applied'), 'success');
                this.httpsStatus = data.status;
            }
        },

        async removeCert() {
            if (!confirm(this.t('msg.remove_cert_confirm'))) return;
            const data = await this.api('/system/https/remove', {
                method: 'DELETE'
            });
            if (data) {
                this.showToast(data.message || this.t('msg.cert_removed'), 'success');
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

        async loadRcloneTasks() {
            const data = await this.api('/rclone/tasks');
            if (data) this.rcloneTasks = data.tasks || [];
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
                this.showToast(data.message || this.t('msg.remote_created'), 'success');
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
                this.showToast(data.message || this.t('msg.remote_updated'), 'success');
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
                this.showToast(data.message || this.t('msg.remote_deleted'), 'success');
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
                this.showToast(data.message || this.t('msg.task_created'), 'success');
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
                this.showToast(data.message || this.t('msg.task_updated'), 'success');
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
                this.showToast(data.message || this.t('msg.task_deleted'), 'success');
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast(this.t('msg.del_failed') + ': ' + data.error, 'error');
            }
        },

        async runTask(id) {
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(id) + '/run', { method: 'POST' });
            if (data && !data.error) {
                this.showToast(data.message || this.t('msg.task_started'), 'success');
                this.loadRcloneTasks();
                // 3秒后刷新日志
                setTimeout(() => { this.loadRcloneTasks(); this.loadRcloneLogs(); }, 3000);
            } else if (data && data.error) {
                this.showToast(this.t('msg.start_failed') + ': ' + data.error, 'error');
            }
        },

        async toggleTask(id) {
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(id) + '/toggle', { method: 'POST' });
            if (data && !data.error) {
                this.showToast(data.message || this.t('msg.status_toggled'), 'success');
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
                this.showToast(data.message || this.t('msg.started'), 'success');
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
