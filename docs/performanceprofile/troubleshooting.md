# Troubleshooting

## Verifying the operator deployment

- check existence and status of the Node Tuning Operator `Deployment`  
  `$ oc get deployment -A | grep cluster-node-tuning-operator`
- check existence and status of the Node Tuning Operator `Pod`  
  `$ oc get pod -A | grep cluster-node-tuning-operator-`
- check logs of the Node Tuning Operator `Pod`  
  `$ oc logs -n ... `

- check general status of the cluster (failed or pending pods)  
  `$ oc get pod -A | grep -vE "Running|Completed"`

## Debugging performance tuning

- check existence and status of the `PerformanceProfile`  
  `$ oc get performanceprofile`
- check logs of the Node Tuning Operator`Pod`  
  `$ oc logs -n ... `
- check `MachineConfigs`, `MachineConfigPools` and `Nodes`  
  `$ oc get mc,mcp,nodes -o=wide`
- check machine config damons of the relevant nodes  
  `$ oc get pod -A -o=wide | grep machine-config-daemon`  
  `$ oc logs -n ... `
- check tuned damons of the relevant nodes  
  `$ oc get pod -A -o=wide | grep tuned`  
  `$ oc logs -n ... `  
- check logs of cluster-node-tuning-operator `Pod`  
  `$ oc logs -n ... `

## Configuration hotfixes

In case a performance configuration needs to be amended please refer to [configuration hotfixes.](./configuration_hotfixes.md) 