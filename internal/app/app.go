package app

import (
	"app-store-server/internal/constants"
	"app-store-server/internal/es"
	"app-store-server/internal/gitapp"
	"app-store-server/internal/helm"
	"app-store-server/internal/mongo"
	"app-store-server/pkg/models"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/golang/glog"
	"gopkg.in/yaml.v3"
)

func Init() error {
	err := UpdateAppInfosToDB()
	if err != nil {
		glog.Warningf("%s", err.Error())
		return err
	}

	go pullAndUpdateLoop()

	return nil
}

func pullAndUpdateLoop() {
	for {
		time.Sleep(time.Duration(1) * time.Minute)
		err := GitPullAndUpdate()
		if err != nil {
			glog.Warningf("%s", err.Error())
		}
	}
}

func GitPullAndUpdate() error {
	err := gitapp.Pull()
	if err == git.NoErrAlreadyUpToDate {
		glog.Infof("info:%s", err.Error())
		return nil
	}
	if err != nil {
		glog.Warningf("git pull err:%s", err.Error())
		return err
	}

	err = gitapp.GetLastCommitHashAndUpdate()
	if err != nil {
		glog.Warningf("GetLastCommitHashAndUpdate err:%s", err.Error())
	}

	return UpdateAppInfosToDB()

	//todo check app infos in mongo if not exist in local, then del it
	//or del by lastCommitHash old
}

func ReadAppInfo(dirName string) (*models.ApplicationInfo, error) {
	cfgFileName := path.Join(constants.AppGitLocalDir, dirName, constants.AppCfgFileName)

	f, err := os.Open(cfgFileName)
	if err != nil {
		glog.Warningf("%s", err.Error())
		return nil, err
	}

	info, err := ioutil.ReadAll(f)
	if err != nil {
		glog.Warningf("%s", err.Error())
		return nil, err
	}

	var appCfg models.AppConfiguration
	if err = yaml.Unmarshal(info, &appCfg); err != nil {
		glog.Warningf("%s", err.Error())
		return nil, err
	}

	return appCfg.ToAppInfo(), nil
}

func UpdateAppInfosToDB() error {
	infos, err := GetAppInfosFromGitDir(constants.AppGitLocalDir)
	if err != nil {
		glog.Warningf("GetAppInfosFromGitDir %s err:%s", constants.AppGitLocalDir, err.Error())
		return err
	}

	err = UpdateAppInfosToMongo(infos)
	if err != nil {
		glog.Warningf("%s", err.Error())
		return err
	}

	go es.SyncInfoFromMongo()

	return nil
}

func UpdateAppInfosToMongo(infos []*models.ApplicationInfo) error {
	for _, info := range infos {
		err := mongo.UpsertAppInfoToDb(info)
		if err != nil {
			glog.Warningf("mongo.UpsertAppInfoToDb err:%s", err.Error())
			continue
		}
	}

	return nil
}

func GetAppInfosFromGitDir(dir string) (infos []*models.ApplicationInfo, err error) {
	charts, err := ioutil.ReadDir(dir)
	if err != nil {
		glog.Warningf("read dir %s error: %s", dir, err.Error())
		return nil, err
	}

	for _, c := range charts {
		if !c.IsDir() {
			continue
		}

		if strings.HasPrefix(c.Name(), ".") {
			continue
		}

		// read app info from chart
		appInfo, err := ReadAppInfo(c.Name())
		if err != nil {
			glog.Warningf("app chart reading error: %s", err.Error())
			continue
		}

		//helm package
		appInfo.ChartName, err = helmPackage(c.Name())
		if err != nil {
			glog.Warningf("app chart reading error: %s", err.Error())
			continue
		}

		glog.Infof("name:%s, version:%s, chartName:%s\n", c.Name(), appInfo.Version, appInfo.ChartName)

		//update info to db
		appInfo.LastCommitHash, err = gitapp.GetLastHash()
		if err != nil {
			glog.Warningf("GetLastHash error: %s", err.Error())
		}
		appInfo.CreateTime, err = gitapp.GetCreateTimeSecond(constants.AppGitLocalDir, c.Name())
		if err != nil {
			glog.Warningf("GetCreateTimeSecond error: %s", err.Error())
		}
		appInfo.UpdateTime, err = gitapp.GetLastUpdateTimeSecond(constants.AppGitLocalDir, c.Name())
		if err != nil {
			glog.Warningf("GetLastUpdateTimeSecond error: %s", err.Error())
		}
		infos = append(infos, appInfo)
	}

	//index charts directory
	err = helm.IndexHelm(constants.RepoName, constants.RepoUrl, constants.AppGitZipLocalDir)
	if err != nil {
		glog.Warningf("IndexHelm error: %s", err.Error())
		return infos, err
	}

	return infos, nil
}

func helmPackage(name string) (string, error) {
	src := path.Join(constants.AppGitLocalDir, name)
	return helm.PackageHelm(src, constants.AppGitZipLocalDir)
}
