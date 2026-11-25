[Unit]
Description=nas-photos (APP-SPEC 家庭照片应用)
After=network.target

[Service]
# ExecStart 路径与核心落盘约定一致（web/modules/apps/registry.go appProgramDir +
# lifecycle.go writeAppFiles）：安装后程序目录是 /var/lib/nas/apps/nas-photos/，
# 包内 app/ 子树原样写入该目录下，所以二进制落在 <programDir>/app/nas-photos。
ExecStart=/var/lib/nas/apps/nas-photos/app/nas-photos
Environment=PORT=__PORT__
Environment=DATA_DIR=__DATA_DIR__
Environment=NAS_SECRET=__NAS_SECRET__
# manifest permissions.storage 声明的池内路径——核心尚无 __STORAGE__ 模板变量
# （M1 只落地 PORT/DATA_DIR/NAS_SECRET，见 web/modules/apps/unit.go），本应用
# 直接以 manifest 声明路径为约定值（main.go 对应默认值一致），核心只负责把该
# 路径纳入 ReadWritePaths 授权（lifecycle.go installReadWritePaths）与 chown。
Environment=STORAGE_DIR=/data/nas1/photos
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
