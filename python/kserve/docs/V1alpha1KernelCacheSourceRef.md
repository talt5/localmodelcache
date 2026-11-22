# V1alpha1KernelCacheSourceRef

KernelCacheSourceRef identifies the source Deployment for a capture.
## Properties
Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**kind** | **str** | Kind is the kind of the source workload. Captures come from Deployments whose Pod template sets the serving.kserve.io/kernelcache-supported annotation. | [default to '']
**name** | **str** | Name is the name of the source workload. | [default to '']

[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


