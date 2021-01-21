# coding: utf-8

from __future__ import absolute_import
from datetime import date, datetime  # noqa: F401

from typing import List, Dict  # noqa: F401

from rest.models.base_model_ import Model
from rest.models.share_definition import ShareDefinition
from rest.models.share_mount_definition import ShareMountDefinition
from rest import util

from rest.models.share_definition import ShareDefinition  # noqa: E501
from rest.models.share_mount_definition import ShareMountDefinition  # noqa: E501

class ShareMountList(Model):
    """NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).

    Do not edit the class manually.
    """

    def __init__(self, share=None, mount_list=None):  # noqa: E501
        """ShareMountList - a model defined in OpenAPI

        :param share: The share of this ShareMountList.  # noqa: E501
        :type share: ShareDefinition
        :param mount_list: The mount_list of this ShareMountList.  # noqa: E501
        :type mount_list: List[ShareMountDefinition]
        """
        self.openapi_types = {
            'share': ShareDefinition,
            'mount_list': List[ShareMountDefinition]
        }

        self.attribute_map = {
            'share': 'share',
            'mount_list': 'mountList'
        }

        self._share = share
        self._mount_list = mount_list

    @classmethod
    def from_dict(cls, dikt) -> 'ShareMountList':
        """Returns the dict as a model

        :param dikt: A dict.
        :type: dict
        :return: The ShareMountList of this ShareMountList.  # noqa: E501
        :rtype: ShareMountList
        """
        return util.deserialize_model(dikt, cls)

    @property
    def share(self):
        """Gets the share of this ShareMountList.


        :return: The share of this ShareMountList.
        :rtype: ShareDefinition
        """
        return self._share

    @share.setter
    def share(self, share):
        """Sets the share of this ShareMountList.


        :param share: The share of this ShareMountList.
        :type share: ShareDefinition
        """

        self._share = share

    @property
    def mount_list(self):
        """Gets the mount_list of this ShareMountList.


        :return: The mount_list of this ShareMountList.
        :rtype: List[ShareMountDefinition]
        """
        return self._mount_list

    @mount_list.setter
    def mount_list(self, mount_list):
        """Sets the mount_list of this ShareMountList.


        :param mount_list: The mount_list of this ShareMountList.
        :type mount_list: List[ShareMountDefinition]
        """

        self._mount_list = mount_list