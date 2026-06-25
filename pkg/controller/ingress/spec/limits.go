package spec

// LimitTargetPools is the maximum amount of target pools allowed in the ALB API.
// Because of how we create target pools per path this is the most limiting factor right now and we don't have to check limits for paths.
const LimitTargetPools = 20
