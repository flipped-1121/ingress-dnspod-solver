package main

import (
	"fmt"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	dnspod "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/dnspod/v20210323"
	"go.uber.org/zap"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"os"
)

var (
	logger       *zap.Logger
	version      string
	policy       string
	domain       string
	secretId     string
	secretKey    string
	recordValue  string
	dnsPodClient *dnspod.Client
	clientSet    *kubernetes.Clientset
)

func initLogger() {
	logger, _ = zap.NewDevelopment()
	defer func(logger *zap.Logger) {
		err := logger.Sync()
		if err != nil {
			fmt.Println("Logger sync error: ", err)
		}
	}(logger)
}

func initCheck() {
	version = os.Getenv("VERSION")
	if version == "" {
		version = "2021-03-23"
	}
	policy = os.Getenv("POLICY")
	if policy == "" {
		policy = "retain"
	}
	domain = os.Getenv("DOMAIN")
	if domain == "" {
		logger.Error("Please set the environment variable `DOMAIN`")
		os.Exit(1)
	}
	secretId = os.Getenv("TENCENT_SECRET_ID")
	secretKey = os.Getenv("TENCENT_SECRET_KEY")
	if secretId == "" || secretKey == "" {
		logger.Error("Please set the environment variables `TENCENT_SECRET_ID` and `TENCENT_SECRET_KEY`")
		os.Exit(1)
	}
	recordValue = os.Getenv("RECORD_VALUE")
	if recordValue == "" {
		logger.Error("Please set the environment variable `RECORD_VALUE`")
		os.Exit(1)
	}

	logger.Info("---------------------------------")
	logger.Info("Version" + ": " + version)
	logger.Info("Policy" + ": " + policy)
	logger.Info("Domain" + ": " + domain)
	logger.Info("SecretId" + ": " + secretId)
	logger.Info("SecretKey: *****")
	logger.Info("RecordValue" + ": " + recordValue)
	logger.Info("---------------------------------")
}

func getRecordDict() map[string]uint64 {
	recordDict := make(map[string]uint64)
	request := dnspod.NewDescribeRecordListRequest()
	request.Domain = common.StringPtr(domain)
	request.RecordType = common.StringPtr("A")
	request.Offset = common.Uint64Ptr(0)
	request.Limit = common.Uint64Ptr(10)

	// 返回的 response 是一个 DescribeRecordListResponse 的实例，与请求对象对应
	response, err := dnsPodClient.DescribeRecordList(request)
	if err != nil {
		logger.Error(err.Error())
	}
	for i := 0; i < len(response.Response.RecordList); i++ {
		recordDict[*response.Response.RecordList[i].Name] = *response.Response.RecordList[i].RecordId
	}
	domainCount := *response.Response.RecordCountInfo.ListCount
	domainTotal := *response.Response.RecordCountInfo.TotalCount
	for domainCount < domainTotal {
		request.Offset = common.Uint64Ptr(domainCount)
		response, err = dnsPodClient.DescribeRecordList(request)
		if err != nil {
			logger.Error(err.Error())
		}
		for i := 0; i < len(response.Response.RecordList); i++ {
			recordDict[*response.Response.RecordList[i].Name] = *response.Response.RecordList[i].RecordId
		}
		domainCount += *response.Response.RecordCountInfo.ListCount
	}

	logger.Info("RecordDict: ", zap.Any("RecordDict", recordDict))
	return recordDict
}

func createRecord(subDomain string) {
	request := dnspod.NewCreateRecordRequest()
	request.Domain = common.StringPtr(domain)
	request.SubDomain = common.StringPtr(subDomain)
	request.RecordType = common.StringPtr("A")
	request.RecordLine = common.StringPtr("默认")
	request.Value = common.StringPtr(recordValue)

	_, err := dnsPodClient.CreateRecord(request)
	if err != nil {
		logger.Error(err.Error())
	}
}

func updateRecord(recordId uint64, subDomain string) {
	request := dnspod.NewModifyRecordRequest()
	request.Domain = common.StringPtr(domain)
	request.RecordId = common.Uint64Ptr(recordId)
	request.SubDomain = common.StringPtr(subDomain)
	request.RecordType = common.StringPtr("A")
	request.RecordLine = common.StringPtr("默认")
	request.Value = common.StringPtr(recordValue)

	_, err := dnsPodClient.ModifyRecord(request)
	if err != nil {
		logger.Error(err.Error())
	}
}

func deleteRecord(recordId uint64) {
	request := dnspod.NewDeleteRecordRequest()
	request.Domain = common.StringPtr(domain)
	request.RecordId = common.Uint64Ptr(recordId)

	_, err := dnsPodClient.DeleteRecord(request)
	if err != nil {
		logger.Error(err.Error())
	}
}

func addHandler(obj interface{}) {
	logger.Info("Detect Ingress Add Event")
	recordDict := getRecordDict()
	addIngress := obj.(*networkingv1.Ingress)
	for i := 0; i < len(addIngress.Spec.Rules); i++ {
		customDomain := addIngress.Spec.Rules[i].Host
		// customDomain 判断是否以 domain 结尾
		if customDomain[len(customDomain)-len(domain):] != domain {
			continue
		}
		subDomain := customDomain[:len(customDomain)-len(domain)-1]
		if _, ok := recordDict[subDomain]; !ok {
			createRecord(subDomain)
		} else {
			if policy == "update" {
				updateRecord(recordDict[subDomain], subDomain)
			}
		}
	}
}

func updateHandler(oldObj, newObj interface{}) {
	logger.Info("Detect Ingress Update Event")
	recordDict := getRecordDict()
	newIngress := newObj.(*networkingv1.Ingress)
	for i := 0; i < len(newIngress.Spec.Rules); i++ {
		customDomain := newIngress.Spec.Rules[i].Host
		// customDomain 判断是否以 domain 结尾
		if customDomain[len(customDomain)-len(domain):] != domain {
			continue
		}
		subDomain := customDomain[:len(customDomain)-len(domain)-1]
		if _, ok := recordDict[subDomain]; !ok {
			createRecord(subDomain)
		} else {
			updateRecord(recordDict[subDomain], subDomain)
		}
	}
}

func deleteHandler(obj interface{}) {
	logger.Info("Detect Ingress Delete Event")
	recordDict := getRecordDict()
	deleteIngress := obj.(*networkingv1.Ingress)
	for i := 0; i < len(deleteIngress.Spec.Rules); i++ {
		customDomain := deleteIngress.Spec.Rules[i].Host
		// customDomain 判断是否以 domain 结尾
		if customDomain[len(customDomain)-len(domain):] != domain {
			continue
		}
		subDomain := customDomain[:len(customDomain)-len(domain)-1]
		if _, ok := recordDict[subDomain]; ok {
			deleteRecord(recordDict[subDomain])
		}
	}
}

func k8sInformer() {
	// 创建共享 Informer 工厂
	factory := informers.NewSharedInformerFactory(clientSet, 0)

	// 获取 Informer Ingress watcher
	ingressInformer := factory.Networking().V1().Ingresses().Informer()

	// 添加事件处理器来监听 Ingress 资源的变化
	_, err := ingressInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    addHandler,
		UpdateFunc: updateHandler,
		DeleteFunc: deleteHandler,
	})
	if err != nil {
		return
	}

	// 启动 informer 并等待缓存同步
	stopCh := make(chan struct{})
	defer close(stopCh)
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	// 阻塞，持续监听事件
	<-stopCh
}

func main() {
	// 初始化
	initLogger()
	initCheck()

	// 创建 DNSPod 客户端
	credential := common.NewCredential(secretId, secretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "dnspod.tencentcloudapi.com"
	dnsPodClient, _ = dnspod.NewClient(credential, "", cpf)

	// 创建 Kubernetes 客户端
	var config *rest.Config
	var err error
	// 判断是否在集群内部
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		config, err = rest.InClusterConfig()
		if err != nil {
			logger.Panic(err.Error())
		}
	} else {
		if _, err = os.Stat("./.kube/config"); os.IsNotExist(err) {
			logger.Panic("Please set the kubeconfig file")
		}
		config, err = clientcmd.BuildConfigFromFlags("", "./.kube/config")
	}
	clientSet, err = kubernetes.NewForConfig(config)
	if err != nil {
		logger.Panic("Failed to create clientSet: ", zap.Error(err))
	}
	k8sInformer()
}
