function nasPanel() {
    return {
        token: localStorage.getItem('nas_token') || '',
        page: 'dashboard',
        loading: false,
        loginError: '',
        loginForm: { username: '', password: '' },
        dashboard: {},
        services: [],
        users: [],
        storage: {},
        smartStatus: '',
        firewallStatus: '',
        firewallForm: { port: '', proto: 'tcp' },
        monitor: {},
        monitorTimer: null,
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
        wizardMode: '',
        wizardLoading: false,
        allDisks: [],
        progressSteps: [],
        progressShow: false,
        progressTitle: '',
        // Storage overview
        storageOverview: {},
        // Shared folders
        sharedFolders: [],
        showAddFolder: false,
        folderForm: { pool: '', name: '', permission: 'readwrite', valid_users: '', recycle_bin: false, nfs: false },
        showFolderPerm: false,
        folderPermForm: { name: '', path: '', pool: '', permission: 'readwrite', valid_users: '', recycle_bin: false },
        // Pool extend
        showExtendPool: false,
        // Backup
        backups: [],
        backupLoading: false,
        // System settings
        sysNetwork: '',
        sysTime: '',
        hostnameForm: { hostname: '' },
        sysSSHConfig: '',
        sysSysctl: '',
        sysUpdates: '',
        sysEnabledServices: '',
        logsModal: false,
        logsService: '',
        logsContent: '',
        addUserModal: false,
        addUserForm: { username: '', password: '' },
        pwdModal: false,
        pwdUser: '',
        pwdForm: { password: '' },
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
                return res.json();
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
                case 'users': this.loadUsers(); break;
                case 'diskmgmt': this.loadStorageOverview(); this.loadWizardStatus(); this.loadSharedFolders(); break;
                case 'firewall': this.loadFirewall(); break;
                case 'monitor': this.initMonitorRefresh(); this.loadAlertConfig(); break;
                case 'config': this.loadEnvConfig(); break;
                case 'system': break;
                case 'backup': this.loadBackups(); break;
            }
        },

        async loadDashboard() {
            const data = await this.api('/dashboard');
            if (data) this.dashboard = data;
        },

        async loadServices() {
            const data = await this.api('/services');
            if (data) this.services = data.services || [];
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
            if (data) this.firewallStatus = data;
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

        async firewallAction(action) {
            if (!this.firewallForm.port) {
                this.showToast('请输入端口号', 'error');
                return;
            }
            const data = await this.api(`/firewall/${action}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `port=${encodeURIComponent(this.firewallForm.port)}&proto=${encodeURIComponent(this.firewallForm.proto)}`
            });
            if (data) {
                this.showToast(data.message || '操作成功', 'success');
                this.loadFirewall();
            }
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
                // Also load full disk list for display
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
                valid_users: this.folderForm.valid_users,
                recycle_bin: this.folderForm.recycle_bin ? 'yes' : '',
                nfs: this.folderForm.nfs ? 'yes' : ''
            });
            const data = await this.api('/disk/folders/create', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(data.message || '文件夹已创建', 'success');
                this.showAddFolder = false;
                this.folderForm = { pool: '', name: '', permission: 'readwrite', valid_users: '', recycle_bin: false, nfs: false };
                this.loadSharedFolders();
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

        // System settings
        async loadSysNetwork() {
            this.sysNetwork = '加载中...';
            const data = await this.api('/system/network');
            if (data) this.sysNetwork = data;
        },

        async loadSysTime() {
            this.sysTime = '加载中...';
            const data = await this.api('/system/time');
            if (data) this.sysTime = data;
        },

        async setHostname() {
            if (!this.hostnameForm.hostname) {
                this.showToast('请输入主机名', 'error');
                return;
            }
            const data = await this.api('/system/hostname', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `hostname=${encodeURIComponent(this.hostnameForm.hostname)}`
            });
            if (data) {
                this.showToast(data.message || '修改成功', 'success');
                this.hostnameForm.hostname = '';
            }
        },

        async loadSysSSHConfig() {
            this.sysSSHConfig = '加载中...';
            const data = await this.api('/system/ssh-config');
            if (data) this.sysSSHConfig = data;
        },

        async loadSysSysctl() {
            this.sysSysctl = '加载中...';
            const data = await this.api('/system/sysctl');
            if (data) this.sysSysctl = data;
        },

        async loadSysUpdates() {
            this.sysUpdates = '检查中...';
            const data = await this.api('/system/updates');
            if (data) this.sysUpdates = data;
        },

        async loadSysEnabledServices() {
            this.sysEnabledServices = '加载中...';
            const data = await this.api('/system/services-enabled');
            if (data) this.sysEnabledServices = data;
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
        }
    };
}
