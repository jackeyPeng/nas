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
            { label: 'FTP 被动 (30000:31000)', port: '30000:31000', proto: 'tcp' },
            { label: 'NFS (2049)', port: '2049', proto: 'any' },
            { label: 'WebDAV (8080)', port: '8080', proto: 'tcp' },
            { label: '面板 (8090)', port: '8090', proto: 'tcp' },
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
                    this.loginError = data.error || '登录失败';
                }
            } catch (e) {
                this.loginError = '网络错误: ' + e.message;
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
                case 'system': this.loadSystemOverview(); this.loadComponents(); break;
                case 'backup': this.loadBackups(); break;
                case 'rclone': this.loadRcloneStatus(); this.loadRcloneRemotes(); this.loadRcloneTasks(); this.loadRcloneLogs(); this.loadSharedDirs(); break;
                case 'logs': this.loadAuditLogs(); break;
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
            if (!confirm('确定要安装所有NAS服务？\n\n包括: Samba, NFS, FTP, WebDAV, FileBrowser, S3, Fail2ban\n\n安装过程可能需要几分钟，请耐心等待。')) return;
            this.installingServices = true;
            this.installMsg = '正在安装...';
            const data = await this.api('/services/install', { method: 'POST' });
            if (data) {
                if (data.error) {
                    this.showToast(`安装失败: ${data.error}`, 'error');
                    this.installMsg = `安装失败: ${data.error}`;
                } else {
                    this.installMsg = data.message || '安装完成';
                    this.showToast(data.message || '安装完成', 'success');
                    setTimeout(() => this.loadServices(), 2000);
                }
            } else {
                this.installMsg = '安装失败';
            }
            this.installingServices = false;
        },

        async installService(name) {
            if (!confirm(`确定安装 ${name}？`)) return;
            this.installMsg = `正在安装 ${name}...`;
            const data = await this.api('/services/install', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `service=${encodeURIComponent(name)}`
            });
            if (data) {
                if (data.error) {
                    this.showToast(`安装失败: ${data.error}`, 'error');
                    this.installMsg = `安装 ${name} 失败: ${data.error}`;
                } else {
                    const steps = (data.steps || []).join(' → ');
                    this.showToast(data.message || `${name} 安装完成`, 'success');
                    this.installMsg = `${name}: ${steps || data.message}`;
                    setTimeout(() => this.loadServices(), 2000);
                }
            } else {
                this.installMsg = `安装 ${name} 失败`;
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
            this.smartStatus = '检查中...';
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
                this.showToast(data.message || '操作成功', 'success');
                this.loadServices();
            }
        },

        async showLogs(name) {
            this.logsService = name;
            this.logsContent = '加载中...';
            this.logsModal = true;
            const data = await this.api(`/services/${name}/logs`);
            if (data) this.logsContent = data || '无日志';
        },

        showAddUser() {
            this.addUserForm = { username: '', password: '' };
            this.addUserModal = true;
        },

        async addUser() {
            if (!this.addUserForm.username || !this.addUserForm.password) {
                this.showToast('请填写用户名和密码', 'error');
                return;
            }
            if (this.addUserForm.password.length < 12) {
                this.showToast('密码至少12位', 'error');
                return;
            }
            const data = await this.api('/users', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `username=${encodeURIComponent(this.addUserForm.username)}&password=${encodeURIComponent(this.addUserForm.password)}`
            });
            if (data) {
                this.showToast(data.message || '添加成功', 'success');
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
                this.showToast('密码至少12位', 'error');
                return;
            }
            const data = await this.api(`/users/${this.pwdUser}/password`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `password=${encodeURIComponent(this.pwdForm.password)}`
            });
            if (data) {
                this.showToast(data.message || '修改成功', 'success');
                this.pwdModal = false;
            }
        },

        async deleteUser(username) {
            if (!confirm(`确定删除用户 ${username}？`)) return;
            const data = await this.api(`/users/${username}?delete_data=false`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || '删除成功', 'success');
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
                    this.showToast('请输入用户名', 'error'); return;
                }
                if (this.userWizardForm.password.length < 12) {
                    this.showToast('密码至少12位', 'error'); return;
                }
                if (this.userWizardForm.password !== this.userWizardForm.password2) {
                    this.showToast('两次密码输入不一致', 'error'); return;
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
                this.showToast(data.message || '创建成功', 'success');
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
                this.showToast(data.message || '服务权限已更新', 'success');
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
                this.showToast(data.message || '配额已更新', 'success');
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
                this.showToast('请输入组名', 'error'); return;
            }
            const data = await this.api('/user-groups', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `name=${encodeURIComponent(this.groupForm.name)}&comment=${encodeURIComponent(this.groupForm.comment)}`
            });
            if (data) {
                this.showToast(data.message || '创建成功', 'success');
                this.showGroupModal = false;
                this.loadUserGroups();
            }
        },

        async deleteGroup(name) {
            if (!confirm(`确定删除用户组 ${name}？`)) return;
            const data = await this.api(`/user-groups/${name}`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || '删除成功', 'success');
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
                this.showToast(data.message || '成员已更新', 'success');
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
                this.showToast(data.message || '权限已更新', 'success');
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
                this.showToast('请输入端口号', 'error');
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
                this.showToast(data.message || '规则已添加', 'success');
                this.firewallForm = { port: '', proto: 'tcp', action: 'allow', from: '', comment: '' };
                this.loadFirewall();
            } else if (data && data.error) {
                this.showToast('添加失败: ' + data.error, 'error');
            }
        },

        async deleteFirewallRule(rule) {
            const desc = (rule.proto === 'any' ? rule.port : rule.port + '/' + rule.proto) + (rule.v6 ? ' (v6)' : '');
            if (!confirm(`确定删除规则 #${rule.num}（${desc}）？`)) return;
            const data = await this.api('/firewall/rules/' + rule.num, { method: 'DELETE' });
            if (data && !data.error) {
                this.showToast(data.message || '规则已删除', 'success');
                this.loadFirewall();
            } else if (data && data.error) {
                this.showToast('删除失败: ' + data.error, 'error');
            }
        },

        async toggleFirewall() {
            const enabling = !this.firewall.active;
            if (enabling && !confirm('启用防火墙？会自动放行 SSH(22) 和面板(8090)。')) return;
            if (!enabling && !confirm('确定禁用防火墙？所有端口将完全开放。')) return;
            const data = await this.api('/firewall/' + (enabling ? 'enable' : 'disable'), { method: 'POST' });
            if (data && !data.error) {
                this.showToast(data.message || '操作成功', 'success');
                this.loadFirewall();
            } else if (data && data.error) {
                this.showToast('操作失败: ' + data.error, 'error');
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
                this.showToast(data.message || '保存成功', 'success');
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
                this.showToast('共享名和路径必填', 'error'); return;
            }
            const data = await this.api('/config/samba/share', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams(this.shareForm).toString()
            });
            if (data) {
                this.showToast(data.message || '添加成功', 'success');
                this.showAddShare = false;
                this.shareForm = { name: '', path: '', comment: '', valid_users: '', read_only: false };
                this.loadSambaShares();
            }
        },

        async deleteSambaShare(name) {
            if (!confirm(`确定删除共享 [${name}]？`)) return;
            const data = await this.api(`/config/samba/share?name=${name}`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || '删除成功', 'success');
                this.loadSambaShares();
            }
        },

        async loadVsftpdUsers() {
            const data = await this.api('/config/vsftpd-users');
            if (data) this.vsftpdUsers = data.users || [];
        },

        async addFtpUser() {
            if (!this.ftpUserForm.username) { this.showToast('请输入用户名', 'error'); return; }
            const data = await this.api('/config/vsftpd-users', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `username=${encodeURIComponent(this.ftpUserForm.username)}`
            });
            if (data) {
                this.showToast(data.message || '添加成功', 'success');
                this.ftpUserForm.username = '';
                this.loadVsftpdUsers();
            }
        },

        async removeFtpUser(username) {
            if (!confirm(`确定从 FTP 白名单移除 ${username}？`)) return;
            const data = await this.api(`/config/vsftpd-users?username=${username}`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || '移除成功', 'success');
                this.loadVsftpdUsers();
            }
        },

        async loadConfigFile() {
            if (!this.configFileForm.name) return;
            this.configFileForm.content = '加载中...';
            const data = await this.api(`/config/file?name=${this.configFileForm.name}`);
            if (data) this.configFileForm.content = data.content || '';
        },

        async saveConfigFile() {
            const data = await this.api('/config/file', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `name=${this.configFileForm.name}&content=${encodeURIComponent(this.configFileForm.content)}`
            });
            if (data) this.showToast(data.message || '保存成功', 'success');
        },

        async loadEnabledServices() {
            this.enabledServices = '加载中...';
            const data = await this.api('/config/services');
            if (data) this.enabledServices = data;
        },

        async toggleService(action) {
            if (!this.svcToggleForm.service) { this.showToast('请输入服务名', 'error'); return; }
            const data = await this.api('/config/service-toggle', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `service=${this.svcToggleForm.service}&action=${action}`
            });
            if (data) {
                this.showToast(data.message || '操作成功', 'success');
                this.svcToggleForm.service = '';
            }
        },

        // Disk management
        async loadDiskInfo() {
            this.diskInfo = '加载中...';
            const data = await this.api('/disk/info');
            if (data) this.diskInfo = data;
        },

        async loadDiskMounts() {
            this.diskMounts = '加载中...';
            const data = await this.api('/disk/mounts');
            if (data) this.diskMounts = data;
        },

        async loadDiskLVM() {
            this.diskLVM = '加载中...';
            const data = await this.api('/disk/lvm');
            if (data) this.diskLVM = data;
        },

        async loadDiskIOStat() {
            this.diskIOStat = '测试中...(约3秒)';
            const data = await this.api('/disk/iostat');
            if (data) this.diskIOStat = data;
        },

        async loadDiskSmartDetail() {
            this.diskSmartDetail = '加载中...';
            const data = await this.api('/disk/smart-detail');
            if (data) this.diskSmartDetail = data;
        },

        async loadDiskPartitions() {
            this.diskPartitions = '加载中...';
            const data = await this.api('/disk/partitions');
            if (data) this.diskPartitions = data;
        },

        async diskMkdir() {
            if (!this.diskMkdirForm.path) { this.showToast('请输入路径', 'error'); return; }
            const data = await this.api('/disk/mkdir', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `path=${encodeURIComponent(this.diskMkdirForm.path)}`
            });
            if (data) {
                this.showToast(data.message || '创建成功', 'success');
                this.diskMkdirForm.path = '';
            }
        },

        async diskMount() {
            if (!this.mountForm.device || !this.mountForm.mountpoint) { this.showToast('设备和挂载点必填', 'error'); return; }
            const data = await this.api('/disk/mount', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams(this.mountForm).toString()
            });
            if (data) {
                this.showToast(data.message || '挂载成功', 'success');
                this.loadDiskMounts();
            }
        },

        async diskUnmount() {
            if (!this.unmountForm.target) { this.showToast('请输入挂载点或设备', 'error'); return; }
            const data = await this.api('/disk/unmount', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `target=${encodeURIComponent(this.unmountForm.target)}`
            });
            if (data) {
                this.showToast(data.message || '卸载成功', 'success');
                this.unmountForm.target = '';
                this.loadDiskMounts();
            }
        },

        async diskFormat() {
            if (!this.formatForm.device) { this.showToast('请输入设备名', 'error'); return; }
            if (!confirm(`⚠️ 确定格式化 ${this.formatForm.device} 为 ${this.formatForm.fstype}？数据将全部丢失！`)) return;
            const data = await this.api('/disk/format', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `device=${encodeURIComponent(this.formatForm.device)}&fstype=${this.formatForm.fstype}&confirm=yes`
            });
            if (data) {
                this.showToast(data.message || '格式化成功', 'success');
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
                this.showToast('请填写设备名和挂载点', 'error'); return;
            }
            if (!confirm(`⚠️ 确定对 ${this.quickSetupForm.device} 执行快速配置？\n将格式化为 ${this.quickSetupForm.fstype}，挂载到 ${this.quickSetupForm.mountpoint}\n该设备上的所有数据将丢失！`)) return;
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
                this.showToast('配置完成: ' + (data.steps||[]).join(' → '), 'success');
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
            if (!this.poolCreateForm.devices) { this.showToast('请填写磁盘', 'error'); return; }
            if (!confirm(`⚠️ 确定创建存储池？\n磁盘: ${this.poolCreateForm.devices}\n这些磁盘上的所有数据将被擦除！`)) return;
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
                this.showToast('存储池创建完成: ' + (data.steps||[]).join(' → '), 'success');
                this.loadPoolStatus();
                this.loadDiskStatus();
                this.showCreatePool = false;
            }
        },

        // New: extend pool
        async extendPool() {
            if (!this.poolExtendForm.device) { this.showToast('请填写磁盘', 'error'); return; }
            if (!confirm(`确定将 ${this.poolExtendForm.device} 加入存储池 ${this.poolExtendForm.vg_name}？`)) return;
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
                this.showToast('扩容完成: ' + (data.steps||[]).join(' → '), 'success');
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
                            friendly: '磁盘 ' + id,
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
            const labels = { safety: '数据安全', capacity: '最大容量', performance: '更高性能', balance: '平衡' };
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
            if (!this.wizMode) { this.showToast('请选择存储方案', 'error'); return; }
            if (!this.wizDisks || this.wizDisks.length === 0) { this.showToast('请选择磁盘', 'error'); return; }

            this.wizStep = 5;
            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = '创建存储池';

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
                                    this.progressSteps.push({ name: ev.detail || '完成', status: 'complete' });
                                } else if (ev.status === 'error') {
                                    this.progressSteps.push({ name: ev.step + ': ' + (ev.detail||''), status: 'error' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: '错误: ' + e.message, status: 'error' });
            }
            this.wizardLoading = false;
            setTimeout(() => { this.loadStorageOverview(); this.loadWizardStatus(); this.loadSharedFolders(); }, 2000);
        },
        // Wizard: setup (streaming with progress)
        async wizardSetup(mode) {
            if (!mode) { this.showToast('请选择存储方式', 'error'); return; }
            const modeName = {single:'单盘配置', merge:'容量优先(合并)', separate:'独立模式', raid1:'安全优先(RAID1)'}[mode];
            if (!confirm(`⚠️ 确定执行「${modeName}」？\n\n选中的磁盘上所有数据将被擦除！`)) return;

            // Show progress panel
            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = '存储配置进度';

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
                                    this.progressSteps.push({ name: ev.detail || '完成', status: 'complete' });
                                } else if (ev.status === 'error') {
                                    this.progressSteps.push({ name: ev.step + ': ' + (ev.detail||''), status: 'error' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: '错误: ' + e.message, status: 'error' });
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
                this.showToast('请选择存储空间并输入文件夹名', 'error'); return;
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
                this.showToast(data.message || '文件夹已创建', 'success');
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
            if (!confirm(`⚠️ 确定删除文件夹 ${f.name}？\n路径: ${f.path}\n该文件夹及其中所有数据将被永久删除！`)) return;
            const data = await this.api('/disk/folders/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `path=${encodeURIComponent(f.path)}&confirm=yes`
            });
            if (data) {
                this.showToast(data.message || '文件夹已删除', 'success');
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
                this.showToast(data.message || '权限已更新', 'success');
                this.showFolderPerm = false;
                this.loadSharedFolders();
                this.loadStorageOverview();
            }
        },

        // Pool extend with SSE progress
        async extendPoolStream() {
            if (!this.poolExtendForm.device) { this.showToast('请选择磁盘', 'error'); return; }
            if (!confirm(`确定将 ${this.poolExtendForm.device} 加入存储池？\n该磁盘上的数据将被擦除！`)) return;

            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = '扩容进度';

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
                                    this.progressSteps.push({ name: ev.detail || '完成', status: 'complete' });
                                } else if (ev.status === 'error') {
                                    this.progressSteps.push({ name: ev.step + ': ' + (ev.detail||''), status: 'error' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: '错误: ' + e.message, status: 'error' });
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
            if (!this.raidExpandForm.device) { this.showToast('请选择磁盘', 'error'); return; }
            const raidType = this.raidExpandForm.raidType;
            const warnMsg = raidType === 'RAID1'
                ? `确定将 ${this.raidExpandForm.device} 加入 ${this.raidExpandForm.mdDevice}？\n该磁盘上的数据将被擦除！`
                : `确定将 ${this.raidExpandForm.device} 加入 ${this.raidExpandForm.mdDevice}？\n该磁盘上的数据将被擦除！\n\n⚠️ RAID5/6 扩容会触发数据重构，可能需要数小时！`;
            if (!confirm(warnMsg)) return;

            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = 'RAID 扩容进度';

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
                                    this.progressSteps.push({ name: ev.detail || '完成', status: 'complete' });
                                } else if (ev.status === 'reshaping') {
                                    this.progressSteps.push({ name: ev.detail || '重构中', status: 'complete' });
                                } else if (ev.status === 'error') {
                                    this.progressSteps.push({ name: ev.step + ': ' + (ev.detail||''), status: 'error' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: '错误: ' + e.message, status: 'error' });
            }
            this.wizardLoading = false;
            this.showRAIDExpand = false;
            this.progressTitle = '';
            setTimeout(() => { this.loadStorageOverview(); this.loadWizardStatus(); }, 2000);
        },

        // Wizard: reset storage (streaming)
        async resetStorage() {
            if (!confirm(`⚠️ 警告：重新配置将清除当前所有存储设置！\n\n` +
                `• 卸载所有数据磁盘\n` +
                `• 删除 LVM 卷组/逻辑卷\n` +
                `• 清除 RAID 配置\n` +
                `• 删除 fstab 持久化\n` +
                `• 删除 Samba 共享\n\n` +
                `磁盘上的数据将被保留（仅解除配置），但建议先备份！\n\n` +
                `确定继续吗？`)) return;

            this.wizardLoading = true;
            this.progressSteps = [];
            this.progressShow = true;
            this.progressTitle = '重置存储进度';

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
                                    this.progressSteps.push({ name: ev.detail || '完成', status: 'complete' });
                                }
                            } catch(e) {}
                        }
                    }
                }
            } catch(e) {
                this.progressSteps.push({ name: '错误: ' + e.message, status: 'error' });
            }
            this.wizardLoading = false;
            this.progressTitle = '';
            setTimeout(() => { this.loadStorageOverview(); this.loadWizardStatus(); this.loadSharedFolders(); }, 2000);
        },

        // ===== Pool operations =====

        // Open extend pool dialog for a specific pool
        openExtendPool(pool) {
            if (!pool) {
                this.showToast('请先从存储池列表选择', 'error');
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
                this.showToast('请填写所有字段', 'error'); return;
            }
            if (!confirm(`⚠️ 确定替换磁盘？\n从 ${f.md_device} 移除 ${f.old_device}\n添加 ${f.new_device}\n\n新磁盘上的数据将被擦除！`)) return;

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
                this.showToast(data.message || '替换盘操作完成', 'success');
                if (data.rebuild && data.rebuild.active) {
                    this.showToast('重建进行中: ' + (data.rebuild.progress || '') + '%', 'info');
                }
                this.showReplaceDisk = false;
                this.loadStorageOverview();
            }
        },

        // Trigger scrub on a pool or all pools
        async syncConfigs() {
            if (!confirm('将根据当前文件夹状态重新生成所有服务配置，确定继续？')) return;
            const data = await this.api('/disk/config/sync', { method: 'POST' });
            if (data) {
                this.showToast(data.message || '配置同步完成', 'success');
                const cdata = await this.api('/disk/config/check');
                if (cdata) this.configIssues = cdata;
            }
        },

        async loadPendingOps() {
            const data = await this.api('/disk/pending');
            if (data) this.pendingCount = data.count || 0;
        },

        async applyPending() {
            if (!confirm('确定要应用所有待处理的变更？\n\n这将执行创建/修改/删除操作并更新所有服务配置。')) return;
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
            if (!confirm('确定要放弃所有待处理的变更？')) return;
            const data = await this.api('/disk/pending/discard', { method: 'POST' });
            if (data) {
                this.showToast(data.message || '已清空', 'success');
                this.pendingCount = 0;
            }
        },

        async loadOperationLogs() {
            const data = await this.api('/disk/oplogs?limit=100');
            if (data) this.operationLogs = data.logs || [];
        },

        async clearOperationLogs() {
            if (!confirm('确定要清空操作日志？')) return;
            const data = await this.api('/disk/oplogs/clear', { method: 'POST' });
            if (data) {
                this.showToast(data.message || '日志已清空', 'success');
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
            if (!confirm('确定要清空所有系统操作日志？')) return;
            const data = await this.api('/logs/clear', { 
                method: 'POST', 
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: 'confirm=yes' 
            });
            if (data) {
                this.showToast(data.message || '日志已清空', 'success');
                this.auditLogs = [];
                this.auditLogTotal = 0;
            }
        },

        async scrubPool(pool) {
            const target = pool ? pool.device + ' (' + pool.display_name + ')' : '所有 RAID 阵列';
            if (!confirm(`确定对 ${target} 执行数据清理？\n\n清理过程会扫描所有数据块，期间性能可能略有下降。`)) return;

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
                this.showToast(data.message || '清理已启动', 'success');
                this.loadOperationsLog();
            }
        },

        // Start SMART self-test on all disks
        async startSMARTScan() {
            if (!confirm('确定对所有非系统磁盘执行 SMART 快速检测？\n约需 2 分钟。')) return;

            const params = new URLSearchParams({ type: 'short', confirm: 'yes' });
            const data = await this.api('/disk/smart-scan', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(data.message || 'SMART 检测已启动', 'success');
                this.loadOperationsLog();
            }
        },

        // Delete pool
        async deletePool(pool) {
            if (!pool) return;
            const poolName = pool.display_name || pool.name;
            if (!confirm(`⚠️ 确定删除 ${poolName}？\n\n` +
                `• 所有数据将被永久删除\n` +
                `• 磁盘将被释放为空闲状态\n` +
                `• 此操作不可恢复！\n\n` +
                `输入 "${poolName}" 确认删除：`)) return;

            const confirmName = prompt(`请输入 "${poolName}" 确认删除：`);
            if (confirmName !== poolName) {
                this.showToast('名称不匹配，已取消', 'error');
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
                this.showToast(data.message || '存储池已删除', 'success');
                this.loadStorageOverview();
                this.loadWizardStatus();
                this.loadSharedFolders();
            }
        },

        // System settings — new unified page
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
        },

        async saveHostname() {
            if (!this.sysSettings.hostname) {
                this.showToast('请输入主机名', 'error');
                return;
            }
            const data = await this.api('/system/hostname', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `hostname=${encodeURIComponent(this.sysSettings.hostname)}`
            });
            if (data) this.showToast(data.message || '修改成功', 'success');
        },

        async saveTimezone() {
            const data = await this.api('/system/timezone', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `timezone=${encodeURIComponent(this.sysSettings.timezone)}`
            });
            if (data) this.showToast(data.message || '时区已修改', 'success');
        },

        async saveSSHConfig() {
            const ssh = this.sysSettings.ssh;
            if (!confirm(`确定修改 SSH 配置？\n端口: ${ssh.port}\nRoot登录: ${ssh.permit_root_login ? '允许' : '禁止'}\n密码认证: ${ssh.password_auth ? '允许' : '禁止'}\n\n修改后 SSH 将重启！`)) return;
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
            if (data) this.showToast(data.message || 'SSH 配置已更新', 'success');
        },

        async saveSysctl(param) {
            const data = await this.api('/system/sysctl', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `key=${encodeURIComponent(param.key)}&value=${encodeURIComponent(param.value)}`
            });
            if (data) this.showToast(data.message || '已保存', 'success');
        },

        async runUpdate(action) {
            const msg = action === 'update' ? '正在刷新软件源...' : '正在更新系统，这可能需要几分钟...';
            this.showToast(msg, 'info');
            const data = await this.api('/system/updates', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `action=${action}`
            });
            if (data) {
                this.showToast(data.message || '完成', 'success');
                this.loadSystemOverview();
            }
        },

        async serviceAction(name, action) {
            const actionText = { start: '启动', stop: '停止', restart: '重启', enable: '启用自启', disable: '禁用自启' };
            const data = await this.api('/system/services', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `name=${encodeURIComponent(name)}&action=${action}`
            });
            if (data) {
                this.showToast(data.message || `${actionText[action]}成功`, 'success');
                this.loadSystemOverview();
            }
        },

        async resetSystem() {
            if (!confirm('⚠️ 确定恢复出厂设置？\n\n此操作将：\n- 销毁所有存储配置（LVM/RAID/磁盘签名）\n- 移除所有 Z1 托管共享（Samba + NFS）\n- 删除面板数据库\n- 恢复为出厂状态\n- 数据文件将丢失！\n\n当前配置会自动备份到 /data/backups/')) return;
            if (!confirm('⚠️ 最终确认：此操作不可撤销！\n\n所有存储配置和数据将被清除。\n确定继续？')) return;
            this.resetMsg = '正在重置，请稍候...';
            const data = await this.api('/system/reset', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: 'confirm=yes'
            });
            if (data) {
                if (data.error) {
                    this.showToast(data.error, 'error');
                    this.resetMsg = '失败: ' + data.error;
                } else if (data.status === 'running') {
                    this.showToast('重置已在后台开始，请等待...', 'success');
                    this.resetMsg = '重置中...';
                    // 轮询等待完成
                    await this.waitForReset();
                } else {
                    this.showToast(data.message || '恢复完成', 'success');
                    this.resetMsg = data.message || '恢复完成';
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
                    this.showToast('恢复出厂设置完成', 'success');
                    this.resetMsg = '恢复出厂设置完成';
                    setTimeout(() => { location.reload(); }, 2000);
                    return;
                }
                this.resetMsg = '重置中... (' + (i + 1) * 2 + 's)';
            }
            this.resetMsg = '重置超时，请刷新页面查看';
            this.showToast('重置可能仍在进行中，请刷新页面查看', 'error');
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
                this.showToast('备份已创建', 'success');
                this.loadBackups();
            }
            this.backupLoading = false;
        },

        async restoreBackup(file) {
            if (!confirm('⚠️ 确定从此备份恢复？当前所有 NAS 配置将被覆盖，服务将重启。')) return;
            this.showToast('恢复中，请等待...', 'success');
            const data = await this.api('/backup/restore', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `file=${encodeURIComponent(file)}`
            });
            if (data) {
                this.showToast('恢复完成，服务已重启', 'success');
            }
        },

        async deleteBackup(file) {
            if (!confirm('确定删除此备份文件？')) return;
            const data = await this.api('/backup/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `file=${encodeURIComponent(file)}`
            });
            if (data) {
                this.showToast('备份已删除', 'success');
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
                this.showToast('读取远端配置失败: ' + (data && data.error || ''), 'error');
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
                this.showToast('请填写名称和类型', 'error');
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
                this.showToast(data.message || '远端已创建', 'success');
                this.showAddRemote = false;
                this.remoteForm = this.blankRemoteForm();
                this.loadRcloneRemotes();
            } else if (data && data.error) {
                this.showToast('创建失败: ' + data.error, 'error');
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
                this.showToast(data.message || '远端已更新', 'success');
                this.showAddRemote = false;
                this.editingRemote = '';
                this.loadRcloneRemotes();
            } else if (data && data.error) {
                this.showToast('保存失败: ' + data.error, 'error');
            }
        },

        async deleteRemote(name) {
            if (!confirm('确定删除远端 "' + name + '"？相关同步任务将无法运行。')) return;
            const data = await this.api('/rclone/remotes/' + encodeURIComponent(name), { method: 'DELETE' });
            if (data && !data.error) {
                this.showToast(data.message || '远端已删除', 'success');
                this.loadRcloneRemotes();
            } else if (data && data.error) {
                this.showToast('删除失败: ' + data.error, 'error');
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
                    this.showToast('连接成功: ' + name, 'success');
                } else {
                    this.showToast('连接失败: ' + (data.error || data.message), 'error');
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
                this.showToast('目录已创建: ' + data.path, 'success');
            } else if (data && data.error) {
                this.showToast('创建失败: ' + data.error, 'error');
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
                this.showToast('请填写必填项', 'error');
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
                this.showToast(data.message || '任务已创建', 'success');
                this.showAddTask = false;
                this.taskForm = this.blankTaskForm();
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast('创建失败: ' + data.error, 'error');
            }
        },

        async updateTask() {
            const fullSource = this.taskFullSource();
            const t = this.taskForm;
            if (!t.name || !fullSource || !t.remote || !t.dest_path) {
                this.showToast('请填写必填项', 'error');
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
                this.showToast(data.message || '任务已更新', 'success');
                this.showAddTask = false;
                this.editingTask = null;
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast('保存失败: ' + data.error, 'error');
            }
        },

        async deleteTask(id) {
            if (!confirm('确定删除此同步任务？')) return;
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(id), { method: 'DELETE' });
            if (data && !data.error) {
                this.showToast(data.message || '任务已删除', 'success');
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast('删除失败: ' + data.error, 'error');
            }
        },

        async runTask(id) {
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(id) + '/run', { method: 'POST' });
            if (data && !data.error) {
                this.showToast(data.message || '任务已开始执行', 'success');
                this.loadRcloneTasks();
                // 3秒后刷新日志
                setTimeout(() => { this.loadRcloneTasks(); this.loadRcloneLogs(); }, 3000);
            } else if (data && data.error) {
                this.showToast('启动失败: ' + data.error, 'error');
            }
        },

        async toggleTask(id) {
            const data = await this.api('/rclone/tasks/' + encodeURIComponent(id) + '/toggle', { method: 'POST' });
            if (data && !data.error) {
                this.showToast(data.message || '状态已切换', 'success');
                this.loadRcloneTasks();
            } else if (data && data.error) {
                this.showToast('操作失败: ' + data.error, 'error');
            }
        },

        async clearRcloneLogs() {
            if (!confirm('确定清空所有同步日志？')) return;
            const data = await this.api('/rclone/logs', { method: 'DELETE' });
            if (data && !data.error) {
                this.showToast('日志已清空', 'success');
                this.loadRcloneLogs();
            }
        }
    };
}
