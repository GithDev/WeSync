export enum FolderDirection {
  SendReceive = 'sendreceive',
  SendOnly = 'sendonly',
  ReceiveOnly = 'receiveonly',
}

export enum SyncTrigger {
  Periodic = 'periodic',
  Scheduled = 'scheduled',
  OnChange = 'on_change',
  OnChangePoll = 'on_change_poll',
}

export enum NetworkMode {
  Any = 'any',
  AnyWifi = 'any_wifi',
  TrustedWifi = 'trusted_wifi',
}
