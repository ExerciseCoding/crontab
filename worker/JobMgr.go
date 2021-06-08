package worker

import (
	"context"
	"crontab/common"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/mvcc/mvccpb"
)

//定义任务管理器
type JobMgr struct {
	client *clientv3.Client
	kv     clientv3.KV
	lease  clientv3.Lease
	watcher clientv3.Watcher
}

var (
	//单例
	G_jobMgr *JobMgr
)

func(jobMgr *JobMgr) watchJobs() (err error){
	var (
		getResp *clientv3.GetResponse
		kvpair *mvccpb.KeyValue
		job *common.CronJob
		watchStartRevision int64
		watchChan clientv3.WatchChan
		watchResp clientv3.WatchResponse
		watchEvent *clientv3.Event
		jobName string
		jobEvent *common.JobEvent
	)
	//1.get /cron/jobs/目录下的所有任务，并且获知当前集群的revision
	if getResp,err = jobMgr.kv.Get(context.TODO(),common.JOB_SAVE_DIR,clientv3.WithPrefix()); err != nil {
		return
	}

	for  _, kvpair = range getResp.Kvs {
		//反序列化json得到Job
		if job ,err = common.UnpackJob(kvpair.Value); err == nil {

			jobEvent = common.BuildJobEvent(common.JOB_EVENT_SAVE,job)
			//将任务同步给scheduler(调度协程)
			G_scheduler.PushJobEvent(jobEvent)
		}


	}

	//2.从该revision向后监听变化事件
	go func(){//监听协程

		//从GET时刻的后续版本开始监听变化
		watchStartRevision = getResp.Header.Revision + 1

		//启动监听/cron/jobs/目录的后续变化
		watchChan = jobMgr.watcher.Watch(context.TODO(),common.JOB_SAVE_DIR,clientv3.WithRev(watchStartRevision),clientv3.WithPrefix())

		//处理监听事件
		for watchResp = range watchChan{
			for _, watchEvent = range watchResp.Events{
				switch watchEvent.Type {
				case mvccpb.PUT: //任务保存事件
					if job,err = common.UnpackJob(watchEvent.Kv.Value); err != nil{
						continue
					}
					//构造EVENT
					jobEvent = common.BuildJobEvent(common.JOB_EVENT_SAVE,job)

				case mvccpb.DELETE: //任务删除事件
					//Delete /cron/jobs/job10
					jobName = common.ExtractJobName(string(watchEvent.Kv.Key))
					//构造一个Event
					job = &common.CronJob{
						Name:     jobName,
					}
					jobEvent = common.BuildJobEvent(common.JOB_EVENT_DELETE,job)

					
				
				}

				//向scheduler推送事件

				G_scheduler.PushJobEvent(jobEvent)
			}
		}

	}()
	return
}

//初始化管理器
func InitJobMgr() (err error) {
	var (
		config clientv3.Config
		client *clientv3.Client
		kv     clientv3.KV
		lease  clientv3.Lease
		watcher clientv3.Watcher
	)
	//初始化配置
	config = clientv3.Config{
		Endpoints:   G_config.EtcdEndpoints,                                     //集群地址
		DialTimeout: time.Duration(G_config.EtcdDialTimeout) * time.Millisecond, //连接超时时间
	}

	//建立连接
	if client, err = clientv3.New(config); err != nil {
		return
	}

	//获取KV和Lease的API子集
	kv = clientv3.NewKV(client)
	lease = clientv3.NewLease(client)
	watcher = clientv3.NewWatcher(client)

	//赋值单例
	G_jobMgr = &JobMgr{
		client: client,
		kv:     kv,
		lease:  lease,
		watcher: watcher,
	}

	//启动任务监听
	G_jobMgr.watchJobs()
	return
}